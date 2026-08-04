package rabbitmq

import amqp "github.com/rabbitmq/amqp091-go"

// ConsumerOption configura un consumidor durante su registro.
type ConsumerOption func(*consumer)

// WithConsumerTag establece el identificador base del consumidor. Cuando la
// concurrencia es mayor que uno, el servidor agrega un sufijo numérico.
func WithConsumerTag(tag string) ConsumerOption {
	return func(consumer *consumer) {
		consumer.tag = tag
	}
}

// WithConsumerConcurrency establece cuántos canales y workers independientes
// consumirán la cola. Cada worker tiene su propio canal AMQP.
func WithConsumerConcurrency(concurrency int) ConsumerOption {
	return func(consumer *consumer) {
		consumer.concurrency = concurrency
	}
}

// WithPrefetch configura basic.qos para cada canal consumidor.
func WithPrefetch(count, size int, global bool) ConsumerOption {
	return func(consumer *consumer) {
		consumer.prefetchCount = count
		consumer.prefetchSize = size
		consumer.prefetchGlobal = global
	}
}

// WithAutoAck permite que RabbitMQ confirme la entrega antes de enviarla. Está
// deshabilitado de forma predeterminada porque puede perder mensajes.
func WithAutoAck(enabled bool) ConsumerOption {
	return func(consumer *consumer) {
		consumer.autoAck = enabled
	}
}

// WithExclusive solicita que este sea el único consumidor de la cola. No puede
// combinarse con una concurrencia mayor que uno.
func WithExclusive(enabled bool) ConsumerOption {
	return func(consumer *consumer) {
		consumer.exclusive = enabled
	}
}

// WithConsumerNoWait controla si el driver debe omitir la confirmación de
// registro del broker. Normalmente debe permanecer deshabilitado.
func WithConsumerNoWait(enabled bool) ConsumerOption {
	return func(consumer *consumer) {
		consumer.noWait = enabled
	}
}

// WithConsumerArguments establece argumentos opcionales de basic.consume. El
// mapa se copia para aislar la configuración del llamador.
func WithConsumerArguments(arguments amqp.Table) ConsumerOption {
	copiedArguments := cloneTable(arguments)
	return func(consumer *consumer) {
		consumer.arguments = cloneTable(copiedArguments)
	}
}

// WithRequeueOnError determina si una entrega cuyo handler devuelve error debe
// volver a la cola. Está habilitado de forma predeterminada.
func WithRequeueOnError(enabled bool) ConsumerOption {
	return func(consumer *consumer) {
		consumer.requeueOnError = enabled
	}
}
