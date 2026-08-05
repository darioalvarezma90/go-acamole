package rabbitmq

import (
	"crypto/tls"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ServerOption configura un Server durante su construcción.
type ServerOption func(*Server)

// TopologyConfigurer declara exchanges, colas y bindings antes de iniciar los
// consumidores. Los configuradores se ejecutan en orden sobre un canal propio.
type TopologyConfigurer func(*amqp.Channel) error

// WithAMQPConfig aplica opciones avanzadas del driver oficial. La configuración
// TLS y las colecciones mutables conocidas se copian antes de almacenarse.
func WithAMQPConfig(configuration amqp.Config) ServerOption {
	copiedConfiguration := cloneAMQPConfig(configuration)
	return func(server *Server) {
		server.configuration = cloneAMQPConfig(copiedConfiguration)
	}
}

// WithTLSConfig habilita una configuración TLS verificada para una URI amqps.
// ServerName se obtiene del host de la URI cuando está vacío.
func WithTLSConfig(configuration *tls.Config) ServerOption {
	return func(server *Server) {
		server.tlsConfigured = true
		if configuration == nil {
			server.configuration.TLSClientConfig = nil
			return
		}
		server.configuration.TLSClientConfig = configuration.Clone()
	}
}

// WithHeartbeat establece el intervalo solicitado al broker. Un valor cero
// conserva el comportamiento predeterminado del driver.
func WithHeartbeat(interval time.Duration) ServerOption {
	return func(server *Server) {
		server.configuration.Heartbeat = interval
	}
}

// WithConnectionName establece el nombre visible en RabbitMQ Management.
func WithConnectionName(name string) ServerOption {
	return func(server *Server) {
		properties := cloneTable(server.configuration.Properties)
		if properties == nil {
			properties = amqp.NewConnectionProperties()
		}
		properties.SetClientConnectionName(name)
		server.configuration.Properties = properties
		server.connectionNameConfigured = true
		server.connectionName = name
	}
}

// WithTopologyConfigurer agrega una operación de preparación del topology.
func WithTopologyConfigurer(configure TopologyConfigurer) ServerOption {
	return func(server *Server) {
		server.topologyConfigurers = append(server.topologyConfigurers, configure)
	}
}

// WithErrorHandler recibe errores devueltos por handlers y pánicos recuperados.
func WithErrorHandler(handler ErrorHandler) ServerOption {
	return func(server *Server) {
		server.errorHandlerConfigured = true
		server.errorHandler = handler
	}
}

// cloneAMQPConfig devuelve una copia independiente de la configuración AMQP.
func cloneAMQPConfig(configuration amqp.Config) amqp.Config {
	result := configuration
	result.SASL = append([]amqp.Authentication(nil), configuration.SASL...)
	result.Properties = cloneTable(configuration.Properties)
	if configuration.TLSClientConfig != nil {
		result.TLSClientConfig = configuration.TLSClientConfig.Clone()
	}
	if configuration.Recovery != nil {
		result.Recovery = new(amqp.Recovery)
		*result.Recovery = *configuration.Recovery
		if configuration.Recovery.ReconnectionConfig != nil {
			result.Recovery.ReconnectionConfig = configuration.Recovery.ReconnectionConfig.Clone()
		}
	}
	return result
}
