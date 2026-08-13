package rabbitmq

// EventType identifica un cambio observable en el ciclo de vida del servidor.
type EventType string

const (
	EventServerStarted       EventType = "server_started"
	EventServerStopped       EventType = "server_stopped"
	EventConsumerStarted     EventType = "consumer_started"
	EventConsumerStopped     EventType = "consumer_stopped"
	EventInfrastructureError EventType = "infrastructure_error"
)

// Event describe un cambio de ciclo de vida o un fallo de infraestructura.
// Queue y ConsumerTag sólo se establecen para eventos de consumidor.
type Event struct {
	Type        EventType
	Queue       string
	ConsumerTag string
	Err         error
}

// EventHandler recibe eventos del servidor. Puede invocarse concurrentemente
// desde distintos workers y debe retornar rápidamente.
type EventHandler func(Event)
