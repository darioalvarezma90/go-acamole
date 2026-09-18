package postgresql

import (
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClientOption configura un Client durante su construcción.
type ClientOption func(*Client)

// PoolConfigurer permite aplicar una configuración avanzada sobre un valor
// creado por pgxpool.ParseConfig.
type PoolConfigurer func(*pgxpool.Config) error

// WithConnectionString establece la cadena URL o libpq base. Vacía utiliza PG*
// y los valores predeterminados de pgx. Las opciones individuales prevalecen
// sobre la cadena independientemente del orden en que se registren.
func WithConnectionString(connectionString string) ClientOption {
	return func(client *Client) {
		client.connectionString = connectionString
	}
}

// WithHost establece el host, ruta de socket Unix o lista de hosts separada por
// comas, con la misma sintaxis que el parámetro host de pgx.
func WithHost(host string) ClientOption {
	return withConnectionParam("host", host)
}

// WithPort establece un puerto entre 1 y 65535 para los hosts configurados.
func WithPort(port int) ClientOption {
	return withConnectionParam("port", strconv.Itoa(port))
}

// WithUser establece el usuario de PostgreSQL.
func WithUser(user string) ClientOption {
	return withConnectionParam("user", user)
}

// WithPassword establece la contraseña sin exigir escape URL o libpq. Una
// contraseña vacía conserva el comportamiento de búsqueda en pgpass de pgx.
func WithPassword(password string) ClientOption {
	return withConnectionParam("password", password)
}

// WithDatabase establece el nombre de la base de datos.
func WithDatabase(database string) ClientOption {
	return withConnectionParam("dbname", database)
}

// WithSSLMode establece sslmode (disable, allow, prefer, require, verify-ca o
// verify-full). WithTLSConfig y WithRequireTLS conservan sus garantías finales.
func WithSSLMode(mode string) ClientOption {
	return withConnectionParam("sslmode", mode)
}

// withConnectionParam conserva el último valor explícito de cada parámetro.
func withConnectionParam(key, value string) ClientOption {
	return func(client *Client) {
		if client.connectionParams == nil {
			client.connectionParams = make(map[string]string)
		}
		client.connectionParams[key] = value
	}
}

// WithConnectionCheck determina si NewClient debe ejecutar Ping. Está
// habilitado de forma predeterminada.
func WithConnectionCheck(enabled bool) ClientOption {
	return func(client *Client) {
		client.connectionCheck = enabled
	}
}

// WithApplicationName establece application_name para todas las conexiones.
func WithApplicationName(applicationName string) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if strings.TrimSpace(applicationName) == "" {
			return fmt.Errorf("nombre de aplicacion no puede estar vacío")
		}
		if strings.TrimSpace(applicationName) != applicationName {
			return fmt.Errorf("nombre de aplicacion no puede contener espacios al inicio o al final")
		}
		if config.ConnConfig.RuntimeParams == nil {
			config.ConnConfig.RuntimeParams = make(map[string]string)
		}
		config.ConnConfig.RuntimeParams["application_name"] = applicationName
		return nil
	})
}

// WithConnectTimeout establece el tiempo máximo para abrir una conexión.
func WithConnectTimeout(timeout time.Duration) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if timeout < 0 {
			return fmt.Errorf("timeout de conexion no puede ser negativo")
		}
		config.ConnConfig.ConnectTimeout = timeout
		return nil
	})
}

// WithMaxConnections establece el tamaño máximo del pool.
func WithMaxConnections(connections int32) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if connections < 1 {
			return fmt.Errorf("maximo de conexiones debe ser mayor que cero")
		}
		config.MaxConns = connections
		return nil
	})
}

// WithMinConnections establece el mínimo de conexiones mantenidas por el pool.
func WithMinConnections(connections int32) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if connections < 0 {
			return fmt.Errorf("minimo de conexiones no puede ser negativo")
		}
		config.MinConns = connections
		return nil
	})
}

// WithMinIdleConnections establece el mínimo de conexiones inactivas.
func WithMinIdleConnections(connections int32) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if connections < 0 {
			return fmt.Errorf("minimo de conexiones inactivas no puede ser negativo")
		}
		config.MinIdleConns = connections
		return nil
	})
}

// WithMaxConnectionLifetime establece la vida máxima de una conexión. Cero
// deshabilita el límite.
func WithMaxConnectionLifetime(lifetime time.Duration) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if lifetime < 0 {
			return fmt.Errorf("vida maxima de conexion no puede ser negativa")
		}
		config.MaxConnLifetime = lifetime
		return nil
	})
}

// WithMaxConnectionLifetimeJitter distribuye el vencimiento de conexiones para
// evitar que todas se cierren simultáneamente.
func WithMaxConnectionLifetimeJitter(jitter time.Duration) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if jitter < 0 {
			return fmt.Errorf("jitter de vida maxima no puede ser negativo")
		}
		config.MaxConnLifetimeJitter = jitter
		return nil
	})
}

// WithMaxConnectionIdleTime establece cuánto puede permanecer inactiva una
// conexión. Cero deshabilita el límite.
func WithMaxConnectionIdleTime(idleTime time.Duration) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if idleTime < 0 {
			return fmt.Errorf("tiempo inactivo maximo no puede ser negativo")
		}
		config.MaxConnIdleTime = idleTime
		return nil
	})
}

// WithHealthCheckPeriod establece la frecuencia de revisión del pool.
func WithHealthCheckPeriod(period time.Duration) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if period <= 0 {
			return fmt.Errorf("periodo de health check debe ser mayor que cero")
		}
		config.HealthCheckPeriod = period
		return nil
	})
}

// WithPingTimeout establece el timeout usado por los health checks internos del
// pool. Cero significa que no se agrega un timeout.
func WithPingTimeout(timeout time.Duration) ClientOption {
	return withPoolConfigurer(func(config *pgxpool.Config) error {
		if timeout < 0 {
			return fmt.Errorf("timeout de ping no puede ser negativo")
		}
		config.PingTimeout = timeout
		return nil
	})
}

// WithTLSConfig habilita TLS verificado y elimina fallbacks sin cifrado. Si
// ServerName está vacío se obtiene del host configurado para cada servidor.
func WithTLSConfig(configuration *tls.Config) ClientOption {
	return func(client *Client) {
		client.tlsConfigured = true
		client.requireTLS = true
		if configuration == nil {
			client.tlsConfig = nil
			return
		}
		client.tlsConfig = configuration.Clone()
	}
}

// WithRequireTLS rechaza configuraciones que puedan conectarse sin TLS. Para
// validar también hostname y cadena de confianza use sslmode=verify-full en la
// cadena de conexión o WithTLSConfig.
func WithRequireTLS() ClientOption {
	return func(client *Client) {
		client.requireTLS = true
	}
}

// WithPoolConfigurer agrega una configuración avanzada. Los configuradores se
// ejecutan en orden después de pgxpool.ParseConfig y antes de la validación.
func WithPoolConfigurer(configure PoolConfigurer) ClientOption {
	return withPoolConfigurer(configure)
}

// withPoolConfigurer agrega un configurador interno a las opciones del cliente.
func withPoolConfigurer(configure PoolConfigurer) ClientOption {
	return func(client *Client) {
		client.poolConfigurers = append(client.poolConfigurers, configure)
	}
}
