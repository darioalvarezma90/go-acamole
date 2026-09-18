package postgresql

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// parseConnectionConfig incorpora los parámetros antes de ParseConfig para que
// pgx derive TLS, fallbacks y pgpass a partir del destino definitivo.
func parseConnectionConfig(connectionString string, params map[string]string) (*pgxpool.Config, error) {
	if len(params) == 0 {
		return pgxpool.ParseConfig(connectionString)
	}
	// Orden estable para validación y serialización de parámetros.
	keys := []string{"host", "port", "user", "password", "dbname", "sslmode"}
	for _, key := range keys {
		value, present := params[key]
		if !present {
			continue
		}
		switch key {
		case "host", "user", "dbname":
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%s no puede estar vacío", key)
			}
			if key == "host" && strings.TrimSpace(value) != value {
				return nil, fmt.Errorf("host no puede contener espacios al inicio o al final")
			}
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("puerto debe estar entre 1 y 65535")
			}
		case "sslmode":
			switch value {
			case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
			default:
				return nil, fmt.Errorf("sslmode no válido")
			}
		}
	}

	if strings.HasPrefix(connectionString, "postgres://") || strings.HasPrefix(connectionString, "postgresql://") {
		uri, err := url.Parse(connectionString)
		if err != nil {
			// url.Error incluye la URL original, que puede contener credenciales.
			if urlError, ok := err.(*url.Error); ok {
				return nil, urlError.Err
			}
			return nil, err
		}
		// pgx interpreta '+' literalmente en las URI, a diferencia de net/url.
		// Protegerlo antes de decodificar y emitir los espacios como %20.
		uri.RawQuery = strings.ReplaceAll(uri.RawQuery, "+", "%2B")
		query := uri.Query()
		for _, key := range keys {
			if value, present := params[key]; present {
				if key == "dbname" {
					query.Del("database") // pgx acepta ambos nombres para la misma clave.
				}
				query.Set(key, value)
			}
		}
		uri.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
		return pgxpool.ParseConfig(uri.String())
	}

	var dsn strings.Builder
	dsn.WriteString(connectionString)
	escape := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	for _, key := range keys {
		if value, present := params[key]; present {
			dsn.WriteString(" " + key + "='" + escape.Replace(value) + "'")
		}
	}
	return pgxpool.ParseConfig(dsn.String())
}

// validatePoolConfig comprueba los límites y tiempos configurados para el pool.
func validatePoolConfig(config *pgxpool.Config) error {
	if config == nil || config.ConnConfig == nil {
		return fmt.Errorf("configuracion del pool no puede ser nil")
	}
	if config.MaxConns < 1 {
		return fmt.Errorf("maximo de conexiones debe ser mayor que cero")
	}
	if config.MinConns < 0 {
		return fmt.Errorf("minimo de conexiones no puede ser negativo")
	}
	if config.MinIdleConns < 0 {
		return fmt.Errorf("minimo de conexiones inactivas no puede ser negativo")
	}
	if config.MinConns > config.MaxConns {
		return fmt.Errorf("minimo de conexiones no puede superar el maximo")
	}
	if config.MinIdleConns > config.MaxConns {
		return fmt.Errorf("minimo de conexiones inactivas no puede superar el maximo")
	}
	if config.MaxConnLifetime < 0 || config.MaxConnLifetimeJitter < 0 || config.MaxConnIdleTime < 0 {
		return fmt.Errorf("duraciones de vida del pool no pueden ser negativas")
	}
	if config.HealthCheckPeriod <= 0 {
		return fmt.Errorf("periodo de health check debe ser mayor que cero")
	}
	if config.PingTimeout < 0 {
		return fmt.Errorf("timeout de ping no puede ser negativo")
	}
	if config.ConnConfig.ConnectTimeout < 0 {
		return fmt.Errorf("timeout de conexion no puede ser negativo")
	}
	return nil
}

// applyTLSConfig aplica una copia verificada de TLS al servidor principal y sus fallbacks.
func applyTLSConfig(poolConfig *pgxpool.Config, base *tls.Config) error {
	if poolConfig == nil || poolConfig.ConnConfig == nil {
		return fmt.Errorf("configuracion del pool no puede ser nil")
	}
	if base == nil {
		return fmt.Errorf("configuracion tls no puede ser nil")
	}
	if base.InsecureSkipVerify {
		return fmt.Errorf("InsecureSkipVerify no está permitido")
	}
	if base.MinVersion != 0 && base.MinVersion < tls.VersionTLS12 {
		return fmt.Errorf("version minima tls debe ser 1.2 o superior")
	}
	if base.MaxVersion != 0 && base.MaxVersion < tls.VersionTLS12 {
		return fmt.Errorf("version maxima tls debe permitir 1.2 o superior")
	}
	if base.MinVersion != 0 && base.MaxVersion != 0 && base.MinVersion > base.MaxVersion {
		return fmt.Errorf("version minima tls no puede superar la maxima")
	}
	if base.ServerName != "" && strings.TrimSpace(base.ServerName) != base.ServerName {
		return fmt.Errorf("ServerName tls no puede contener espacios al inicio o al final")
	}

	securePrimary, err := tlsConfigForHost(base, poolConfig.ConnConfig.Host)
	if err != nil {
		return err
	}
	poolConfig.ConnConfig.TLSConfig = securePrimary

	seen := map[string]struct{}{
		fallbackKey(poolConfig.ConnConfig.Host, poolConfig.ConnConfig.Port): {},
	}
	secureFallbacks := make([]*pgconn.FallbackConfig, 0, len(poolConfig.ConnConfig.Fallbacks))

	for _, fallback := range poolConfig.ConnConfig.Fallbacks {
		if fallback == nil {
			continue
		}
		key := fallbackKey(fallback.Host, fallback.Port)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		fallbackTLS, err := tlsConfigForHost(base, fallback.Host)
		if err != nil {
			return err
		}
		secureFallbacks = append(secureFallbacks, &pgconn.FallbackConfig{
			Host:      fallback.Host,
			Port:      fallback.Port,
			TLSConfig: fallbackTLS,
		})
	}

	poolConfig.ConnConfig.Fallbacks = secureFallbacks
	return nil
}

// tlsConfigForHost prepara una configuración TLS independiente para un host.
func tlsConfigForHost(base *tls.Config, host string) (*tls.Config, error) {
	configuration := base.Clone()
	if configuration.MinVersion == 0 {
		configuration.MinVersion = tls.VersionTLS12
	}
	if configuration.ServerName != "" {
		return configuration, nil
	}
	if strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("host postgresql no puede estar vacío al configurar tls")
	}
	if strings.HasPrefix(host, "/") || strings.HasPrefix(host, "@") {
		return nil, fmt.Errorf("ServerName tls es obligatorio para sockets unix")
	}
	configuration.ServerName = host
	return configuration, nil
}

// validateTLSRequired comprueba que el servidor principal y sus fallbacks usen TLS.
func validateTLSRequired(config *pgxpool.Config) error {
	if config.ConnConfig.TLSConfig == nil {
		return fmt.Errorf("la conexion principal permite transporte sin tls")
	}
	for index, fallback := range config.ConnConfig.Fallbacks {
		if fallback == nil || fallback.TLSConfig == nil {
			return fmt.Errorf("fallback %d permite transporte sin tls", index)
		}
	}
	return nil
}

// fallbackKey construye una clave estable a partir del host y puerto alternativos.
func fallbackKey(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}
