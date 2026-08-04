package mongodb

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// ClientOption configura un Client durante su construcción.
type ClientOption func(*Client)

// WithConnectionCheck determina si NewClient debe ejecutar Ping.
func WithConnectionCheck(enabled bool) ClientOption {
	return func(client *Client) {
		client.connectionCheck = enabled
	}
}

// WithPingReadPreference establece la preferencia utilizada por Ping.
func WithPingReadPreference(preference *readpref.ReadPref) ClientOption {
	return func(client *Client) {
		client.pingReadPreference = preference
	}
}

// WithAppName establece el nombre de la aplicación enviado a MongoDB.
func WithAppName(appName string) ClientOption {
	return WithDriverOptions(options.Client().SetAppName(appName))
}

// WithStableAPI habilita MongoDB Stable API versión 1.
func WithStableAPI() ClientOption {
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	return WithDriverOptions(options.Client().SetServerAPIOptions(serverAPI))
}

// WithConnectTimeout establece el tiempo máximo para establecer una conexión.
func WithConnectTimeout(timeout time.Duration) ClientOption {
	return WithDriverOptions(options.Client().SetConnectTimeout(timeout))
}

// WithServerSelectionTimeout establece cuánto esperará el driver para encontrar servidor.
func WithServerSelectionTimeout(timeout time.Duration) ClientOption {
	return WithDriverOptions(options.Client().SetServerSelectionTimeout(timeout))
}

// WithOperationTimeout establece un límite predeterminado para operaciones sin deadline.
func WithOperationTimeout(timeout time.Duration) ClientOption {
	return WithDriverOptions(options.Client().SetTimeout(timeout))
}

// WithMinPoolSize establece el número mínimo de conexiones por servidor.
func WithMinPoolSize(size uint64) ClientOption {
	return WithDriverOptions(options.Client().SetMinPoolSize(size))
}

// WithMaxPoolSize establece el número máximo de conexiones por servidor.
func WithMaxPoolSize(size uint64) ClientOption {
	return WithDriverOptions(options.Client().SetMaxPoolSize(size))
}

// WithTLS habilita TLS usando una configuración creada mediante NewTLS.
func WithTLS(configuration *TLS) ClientOption {
	return func(client *Client) {
		client.tlsConfigured = true
		if configuration == nil {
			client.tlsConfig = nil
			return
		}
		client.tlsConfig = configuration.clone()
	}
}

// WithX509Authentication utiliza el certificado cliente TLS para autenticarse.
func WithX509Authentication() ClientOption {
	return WithDriverOptions(options.Client().SetAuth(options.Credential{
		AuthMechanism: "MONGODB-X509",
	}))
}

// WithDriverOptions permite aplicar opciones avanzadas del driver oficial.
func WithDriverOptions(driverOptions ...*options.ClientOptions) ClientOption {
	copiedOptions := cloneDriverOptions(driverOptions)
	return func(client *Client) {
		client.driverOptions = append(
			client.driverOptions,
			cloneDriverOptions(copiedOptions)...,
		)
	}
}

// cloneDriverOptions crea nuevos contenedores ClientOptions para evitar
// conservar los punteros mutables recibidos del llamador. MergeClientOptions
// mantiene la semántica oficial de precedencia del driver.
func cloneDriverOptions(driverOptions []*options.ClientOptions) []*options.ClientOptions {
	copiedOptions := make([]*options.ClientOptions, len(driverOptions))
	for index, driverOption := range driverOptions {
		if driverOption == nil {
			continue
		}
		copiedOptions[index] = options.MergeClientOptions(options.Client(), driverOption)
	}
	return copiedOptions
}
