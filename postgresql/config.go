package postgresql

import (
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

func fallbackKey(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}
