package rabbitmq

import (
	"context"
	"fmt"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Handler procesa una entrega. El servidor confirma el mensaje cuando devuelve
// nil y lo rechaza cuando devuelve un error. El handler no debe llamar Ack o
// Nack directamente.
type Handler func(ctx context.Context, delivery amqp.Delivery) error

// ErrorHandler recibe errores de negocio devueltos por los handlers. Estos
// errores no detienen el servidor; los errores de infraestructura sí hacen que
// Serve termine. Se invoca después de enviar Nack y debe retornar rápidamente.
type ErrorHandler func(error)

// HandlerError describe un error producido al procesar una entrega.
type HandlerError struct {
	Queue       string
	ConsumerTag string
	DeliveryTag uint64
	Err         error
}

func (e *HandlerError) Error() string {
	if e == nil {
		return "error de handler rabbitmq"
	}
	return fmt.Sprintf(
		"handler rabbitmq para cola %q y delivery %d: %v",
		e.Queue,
		e.DeliveryTag,
		e.Err,
	)
}

func (e *HandlerError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type consumer struct {
	queue          string
	handler        Handler
	tag            string
	autoAck        bool
	exclusive      bool
	noWait         bool
	arguments      amqp.Table
	prefetchCount  int
	prefetchSize   int
	prefetchGlobal bool
	concurrency    int
	requeueOnError bool
}

func newConsumer(queue string, handler Handler, opts ...ConsumerOption) (*consumer, error) {
	configuration := &consumer{
		queue:          queue,
		handler:        handler,
		prefetchCount:  1,
		concurrency:    1,
		requeueOnError: true,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(configuration)
		}
	}

	if err := configuration.validate(); err != nil {
		return nil, fmt.Errorf("error de configuracion del consumidor rabbitmq: %w", err)
	}
	return configuration, nil
}

func (c *consumer) validate() error {
	if c == nil {
		return fmt.Errorf("consumidor no puede ser nil")
	}
	if strings.TrimSpace(c.queue) == "" {
		return fmt.Errorf("nombre de cola no puede estar vacío")
	}
	if c.handler == nil {
		return fmt.Errorf("handler no puede ser nil")
	}
	if c.tag != "" && strings.TrimSpace(c.tag) != c.tag {
		return fmt.Errorf("consumer tag no puede contener espacios al inicio o al final")
	}
	if c.concurrency < 1 {
		return fmt.Errorf("concurrencia debe ser mayor que cero")
	}
	if c.exclusive && c.concurrency > 1 {
		return fmt.Errorf("consumidor exclusivo no puede tener concurrencia mayor que uno")
	}
	if c.prefetchCount < 0 {
		return fmt.Errorf("prefetch count no puede ser negativo")
	}
	if c.prefetchSize < 0 {
		return fmt.Errorf("prefetch size no puede ser negativo")
	}
	return nil
}

func (c *consumer) workerTag(index int) string {
	if c.tag == "" || c.concurrency == 1 {
		return c.tag
	}
	return fmt.Sprintf("%s-%d", c.tag, index+1)
}

func cloneTable(table amqp.Table) amqp.Table {
	if table == nil {
		return nil
	}
	result := make(amqp.Table, len(table))
	for key, value := range table {
		result[key] = cloneTableValue(value)
	}
	return result
}

func cloneTableValue(value any) any {
	switch typedValue := value.(type) {
	case amqp.Table:
		return cloneTable(typedValue)
	case []byte:
		return append([]byte(nil), typedValue...)
	case []any:
		result := make([]any, len(typedValue))
		for index, item := range typedValue {
			result[index] = cloneTableValue(item)
		}
		return result
	default:
		return value
	}
}
