package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// IServer define el contrato de un servidor de consumidores RabbitMQ.
//
// El wrapper administra la conexión, los canales de consumo, acknowledgements
// y el apagado, pero expone la conexión oficial para operaciones avanzadas.
type IServer interface {
	// Driver devuelve la conexión nativa del driver oficial.
	Driver() *amqp.Connection

	// RegisterConsumer registra un handler antes de iniciar Serve.
	RegisterConsumer(queue string, handler Handler, opts ...ConsumerOption) error

	// Serve inicia los consumidores y bloquea hasta que el contexto termine, se
	// invoque Shutdown o se produzca un error de infraestructura.
	Serve(ctx context.Context) error

	// Shutdown detiene el consumo, espera los handlers activos y cierra la
	// conexión. Si vence el contexto, fuerza el cierre de la conexión.
	Shutdown(ctx context.Context) error
}

// Valida en tiempo de compilación que Server implementa IServer.
var _ IServer = (*Server)(nil)
