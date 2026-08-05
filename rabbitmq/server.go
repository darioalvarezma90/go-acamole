// Package rabbitmq proporciona un servidor reutilizable de consumidores
// construido sobre el driver oficial amqp091-go.
package rabbitmq

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const defaultCloseTimeout = 5 * time.Second

var (
	// ErrNilContext indica que se recibió un contexto nil.
	ErrNilContext = errors.New("contexto no puede ser nil")

	// ErrServerClosed indica que se intentó usar un servidor cerrado.
	ErrServerClosed = errors.New("servidor rabbitmq cerrado")

	// ErrServerRunning indica que Serve ya se encuentra en ejecución.
	ErrServerRunning = errors.New("servidor rabbitmq en ejecucion")

	// ErrServerAlreadyServed indica que la instancia ya completó Serve y no es
	// reutilizable después de cerrar sus canales.
	ErrServerAlreadyServed = errors.New("servidor rabbitmq ya fue ejecutado")

	// ErrServerUnavailable indica que el wrapper no contiene una conexión válida.
	ErrServerUnavailable = errors.New("servidor rabbitmq no disponible")

	// ErrNoConsumers indica que Serve fue invocado sin consumidores registrados.
	ErrNoConsumers = errors.New("servidor rabbitmq no tiene consumidores")
)

// Server administra una conexión AMQP y canales de consumo dedicados. Puede
// compartirse entre goroutines; los consumidores deben registrarse antes de
// invocar Serve.
type Server struct {
	uri                      string
	configuration            amqp.Config
	connectionName           string
	connectionNameConfigured bool
	tlsConfigured            bool
	topologyConfigurers      []TopologyConfigurer
	errorHandler             ErrorHandler
	errorHandlerConfigured   bool

	driver *amqp.Connection

	stateMutex       sync.Mutex
	consumers        []*consumer
	consumerChannels []*amqp.Channel
	running          bool
	served           bool
	serveCancel      context.CancelFunc
	done             chan struct{}
	doneOnce         sync.Once

	closeMutex sync.Mutex
	closed     atomic.Bool
	closeErr   error
}

// NewServer establece una conexión con RabbitMQ. La conexión confirma que el
// broker es alcanzable; los consumidores no se crean hasta invocar Serve.
func NewServer(uri string, opts ...ServerOption) (*Server, error) {
	server := &Server{
		uri:          uri,
		errorHandler: func(error) {},
		done:         make(chan struct{}),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(server)
		}
	}

	if err := server.validate(); err != nil {
		return nil, fmt.Errorf("error de configuracion del servidor rabbitmq: %w", err)
	}

	driver, err := amqp.DialConfig(server.uri, cloneAMQPConfig(server.configuration))
	if err != nil {
		return nil, fmt.Errorf("error conectando con rabbitmq: %w", err)
	}
	server.driver = driver
	return server, nil
}

// validate comprueba que la configuración interna del servidor sea válida.
func (s *Server) validate() error {
	if s == nil {
		return fmt.Errorf("servidor no puede ser nil")
	}
	if strings.TrimSpace(s.uri) == "" {
		return fmt.Errorf("uri no puede estar vacío")
	}
	parsedURI, err := amqp.ParseURI(s.uri)
	if err != nil {
		return fmt.Errorf("uri inválida: %w", err)
	}
	if s.configuration.Heartbeat < 0 {
		return fmt.Errorf("heartbeat no puede ser negativo")
	}
	if s.connectionNameConfigured {
		if strings.TrimSpace(s.connectionName) == "" {
			return fmt.Errorf("nombre de conexion no puede estar vacío")
		}
		if strings.TrimSpace(s.connectionName) != s.connectionName {
			return fmt.Errorf("nombre de conexion no puede contener espacios al inicio o al final")
		}
	}
	if s.tlsConfigured && s.configuration.TLSClientConfig == nil {
		return fmt.Errorf("configuracion tls no puede ser nil")
	}
	if s.configuration.TLSClientConfig != nil {
		if parsedURI.Scheme != "amqps" {
			return fmt.Errorf("configuracion tls requiere una uri amqps")
		}
		if err := validateTLSConfig(s.configuration.TLSClientConfig); err != nil {
			return err
		}
		if s.configuration.TLSClientConfig.MinVersion == 0 {
			s.configuration.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	if s.errorHandlerConfigured && s.errorHandler == nil {
		return fmt.Errorf("error handler no puede ser nil")
	}
	for index, configure := range s.topologyConfigurers {
		if configure == nil {
			return fmt.Errorf("configurador de topology en posicion %d no puede ser nil", index)
		}
	}
	return nil
}

// validateTLSConfig comprueba que la configuración TLS valide identidad y certificados.
func validateTLSConfig(configuration *tls.Config) error {
	if configuration.InsecureSkipVerify {
		return fmt.Errorf("InsecureSkipVerify no está permitido")
	}
	if configuration.MinVersion != 0 && configuration.MinVersion < tls.VersionTLS12 {
		return fmt.Errorf("version minima tls debe ser 1.2 o superior")
	}
	if configuration.MaxVersion != 0 && configuration.MaxVersion < tls.VersionTLS12 {
		return fmt.Errorf("version maxima tls debe permitir 1.2 o superior")
	}
	if configuration.MinVersion != 0 && configuration.MaxVersion != 0 && configuration.MinVersion > configuration.MaxVersion {
		return fmt.Errorf("version minima tls no puede superar la maxima")
	}
	if configuration.ServerName != "" && strings.TrimSpace(configuration.ServerName) != configuration.ServerName {
		return fmt.Errorf("ServerName tls no puede contener espacios al inicio o al final")
	}
	return nil
}

// Driver devuelve la conexión oficial. No debe cerrarse directamente; el
// propietario del wrapper debe llamar a Shutdown.
func (s *Server) Driver() *amqp.Connection {
	if s == nil {
		return nil
	}
	return s.driver
}

// RegisterConsumer registra un handler. Es seguro registrar desde distintas
// goroutines siempre que Serve todavía no haya comenzado.
func (s *Server) RegisterConsumer(queue string, handler Handler, opts ...ConsumerOption) error {
	if s == nil || s.driver == nil {
		return ErrServerUnavailable
	}
	configuration, err := newConsumer(queue, handler, opts...)
	if err != nil {
		return err
	}

	s.stateMutex.Lock()
	defer s.stateMutex.Unlock()
	if s.closed.Load() {
		return ErrServerClosed
	}
	if s.running {
		return ErrServerRunning
	}
	if s.served {
		return ErrServerAlreadyServed
	}
	if err := validateConsumerCompatibility(s.consumers, configuration); err != nil {
		return err
	}
	s.consumers = append(s.consumers, configuration)
	return nil
}

// validateConsumerCompatibility evita configuraciones incompatibles para una misma cola.
func validateConsumerCompatibility(existingConsumers []*consumer, candidate *consumer) error {
	for _, existing := range existingConsumers {
		if existing == nil || existing.queue != candidate.queue {
			continue
		}
		if existing.exclusive || candidate.exclusive {
			return fmt.Errorf(
				"cola %q no puede combinar consumidores exclusivos con otros consumidores",
				candidate.queue,
			)
		}
	}
	return nil
}

// Serve inicia un canal por worker y bloquea durante el ciclo de vida del
// servidor. Los errores de contexto se devuelven sin envolver.
func (s *Server) Serve(ctx context.Context) (serveErr error) {
	if ctx == nil {
		return ErrNilContext
	}
	if s == nil || s.driver == nil {
		return ErrServerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.stateMutex.Lock()
	if s.closed.Load() {
		s.stateMutex.Unlock()
		return ErrServerClosed
	}
	if s.running {
		s.stateMutex.Unlock()
		return ErrServerRunning
	}
	if s.served {
		s.stateMutex.Unlock()
		return ErrServerAlreadyServed
	}
	if len(s.consumers) == 0 {
		s.stateMutex.Unlock()
		return ErrNoConsumers
	}
	serveContext, cancel := context.WithCancel(ctx)
	s.running = true
	s.served = true
	s.serveCancel = cancel
	consumers := append([]*consumer(nil), s.consumers...)
	s.stateMutex.Unlock()

	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
		s.closeConsumerChannels()
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), defaultCloseTimeout)
		defer cleanupCancel()
		if err := s.closeConnection(cleanupContext); err != nil {
			serveErr = errors.Join(serveErr, err)
		}
		s.stateMutex.Lock()
		s.running = false
		s.serveCancel = nil
		s.stateMutex.Unlock()
		s.signalDone()
	}()

	if err := s.configureTopology(); err != nil {
		return err
	}

	workerErrors := make(chan error, countWorkers(consumers))
	if err := s.startConsumers(serveContext, consumers, workerErrors, &workers); err != nil {
		return err
	}

	connectionClosed := s.driver.NotifyClose(make(chan *amqp.Error, 1))
	select {
	case <-serveContext.Done():
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	case err := <-workerErrors:
		cancel()
		return err
	case connectionError, open := <-connectionClosed:
		cancel()
		if s.closed.Load() {
			return nil
		}
		if open && connectionError != nil {
			return fmt.Errorf("conexion rabbitmq cerrada: %w", connectionError)
		}
		return fmt.Errorf("conexion rabbitmq cerrada")
	}
}

// configureTopology ejecuta en orden los configuradores registrados del topology.
func (s *Server) configureTopology() error {
	if len(s.topologyConfigurers) == 0 {
		return nil
	}
	channel, err := s.driver.Channel()
	if err != nil {
		return fmt.Errorf("error creando canal para topology rabbitmq: %w", err)
	}
	for index, configure := range s.topologyConfigurers {
		if err := configure(channel); err != nil {
			closeErr := channel.Close()
			return errors.Join(
				fmt.Errorf("error aplicando configurador de topology %d: %w", index, err),
				wrapChannelCloseError(closeErr),
			)
		}
	}
	if err := channel.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
		return fmt.Errorf("error cerrando canal de topology rabbitmq: %w", err)
	}
	return nil
}

// startConsumers abre los canales y pone en marcha todos los workers configurados.
func (s *Server) startConsumers(
	ctx context.Context,
	consumers []*consumer,
	workerErrors chan<- error,
	workers *sync.WaitGroup,
) error {
	for _, configuration := range consumers {
		for workerIndex := range configuration.concurrency {
			channel, err := s.driver.Channel()
			if err != nil {
				return fmt.Errorf("error creando canal para cola %q: %w", configuration.queue, err)
			}
			s.addConsumerChannel(channel)

			if !configuration.autoAck && (configuration.prefetchCount > 0 || configuration.prefetchSize > 0) {
				if err := channel.Qos(
					configuration.prefetchCount,
					configuration.prefetchSize,
					configuration.prefetchGlobal,
				); err != nil {
					return fmt.Errorf("error configurando qos para cola %q: %w", configuration.queue, err)
				}
			}

			deliveries, err := channel.ConsumeWithContext(
				ctx,
				configuration.queue,
				configuration.workerTag(workerIndex),
				configuration.autoAck,
				configuration.exclusive,
				false,
				configuration.noWait,
				cloneTable(configuration.arguments),
			)
			if err != nil {
				return fmt.Errorf("error iniciando consumidor para cola %q: %w", configuration.queue, err)
			}

			workers.Add(1)
			ready := make(chan struct{})
			go s.consume(ctx, configuration, deliveries, workerErrors, workers, ready)
			<-ready
		}
	}
	return nil
}

// consume procesa entregas de un worker hasta que su contexto termina.
func (s *Server) consume(
	ctx context.Context,
	configuration *consumer,
	deliveries <-chan amqp.Delivery,
	workerErrors chan<- error,
	workers *sync.WaitGroup,
	ready chan<- struct{},
) {
	defer workers.Done()
	close(ready)
	for delivery := range deliveries {
		if err := s.processDelivery(ctx, configuration, delivery); err != nil {
			workerErrors <- err
			return
		}
	}
	if ctx.Err() == nil && !s.closed.Load() {
		workerErrors <- fmt.Errorf("entregas para cola %q se cerraron inesperadamente", configuration.queue)
	}
}

// processDelivery ejecuta el manejador y aplica la política de confirmación configurada.
func (s *Server) processDelivery(ctx context.Context, configuration *consumer, delivery amqp.Delivery) (result error) {
	handlerErr := callHandler(ctx, configuration.handler, delivery)
	if configuration.autoAck {
		if handlerErr != nil {
			s.reportHandlerError(configuration.queue, delivery, handlerErr)
		}
		return nil
	}
	if handlerErr == nil {
		if err := delivery.Ack(false); err != nil {
			return fmt.Errorf("error confirmando delivery %d de cola %q: %w", delivery.DeliveryTag, configuration.queue, err)
		}
		return nil
	}

	nackErr := delivery.Nack(false, configuration.requeueOnError)
	s.reportHandlerError(configuration.queue, delivery, handlerErr)
	if nackErr != nil {
		return errors.Join(
			&HandlerError{
				Queue:       configuration.queue,
				ConsumerTag: delivery.ConsumerTag,
				DeliveryTag: delivery.DeliveryTag,
				Err:         handlerErr,
			},
			fmt.Errorf("error rechazando delivery rabbitmq: %w", nackErr),
		)
	}
	return nil
}

// callHandler ejecuta un manejador y convierte cualquier pánico en un error.
func callHandler(ctx context.Context, handler Handler, delivery amqp.Delivery) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic en handler: %v", recovered)
		}
	}()
	return handler(ctx, delivery)
}

// reportHandlerError entrega al callback configurado los errores de procesamiento.
func (s *Server) reportHandlerError(queue string, delivery amqp.Delivery, err error) {
	handlerError := &HandlerError{
		Queue:       queue,
		ConsumerTag: delivery.ConsumerTag,
		DeliveryTag: delivery.DeliveryTag,
		Err:         err,
	}
	defer func() {
		_ = recover()
	}()
	s.errorHandler(handlerError)
}

// addConsumerChannel registra un canal para poder cerrarlo durante el apagado.
func (s *Server) addConsumerChannel(channel *amqp.Channel) {
	s.stateMutex.Lock()
	defer s.stateMutex.Unlock()
	s.consumerChannels = append(s.consumerChannels, channel)
}

// closeConsumerChannels cierra todos los canales de consumidores registrados.
func (s *Server) closeConsumerChannels() {
	s.stateMutex.Lock()
	channels := s.consumerChannels
	s.consumerChannels = nil
	s.stateMutex.Unlock()
	for _, channel := range channels {
		if channel != nil {
			_ = channel.Close()
		}
	}
}

// closeConnection cierra de forma idempotente la conexión con el broker.
func (s *Server) closeConnection(ctx context.Context) error {
	s.closeMutex.Lock()
	defer s.closeMutex.Unlock()
	if s.driver == nil || s.driver.IsClosed() {
		return s.closeErr
	}

	var err error
	if deadline, ok := connectionCloseDeadline(ctx); ok {
		err = s.driver.CloseDeadline(deadline)
	} else {
		err = s.driver.Close()
	}
	if err != nil && !errors.Is(err, amqp.ErrClosed) {
		s.closeErr = fmt.Errorf("error cerrando conexion rabbitmq: %w", err)
	}
	return s.closeErr
}

// connectionCloseDeadline obtiene un límite utilizable para cerrar la conexión.
func connectionCloseDeadline(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Now(), true
	}
	if ctx.Err() != nil {
		return time.Now(), true
	}
	return ctx.Deadline()
}

// Shutdown inicia un apagado idempotente. Cancela nuevos consumos y permite que
// los handlers activos observen la cancelación a través de su contexto.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrNilContext
	}

	s.closed.Store(true)
	s.stateMutex.Lock()
	running := s.running
	cancel := s.serveCancel
	s.stateMutex.Unlock()
	if cancel != nil {
		cancel()
	}
	if !running {
		err := s.closeConnection(ctx)
		s.signalDone()
		return err
	}

	select {
	case <-s.doneChannel():
		return s.closeConnection(ctx)
	case <-ctx.Done():
		closeErr := s.closeConnection(ctx)
		return errors.Join(ctx.Err(), closeErr)
	}
}

// signalDone notifica que el ciclo de vida del servidor terminó.
func (s *Server) signalDone() {
	done := s.doneChannel()
	s.doneOnce.Do(func() {
		close(done)
	})
}

// doneChannel devuelve el canal compartido de finalización del servidor.
func (s *Server) doneChannel() chan struct{} {
	s.stateMutex.Lock()
	defer s.stateMutex.Unlock()
	if s.done == nil {
		s.done = make(chan struct{})
	}
	return s.done
}

// countWorkers suma la concurrencia configurada para todos los consumidores.
func countWorkers(consumers []*consumer) int {
	count := 0
	for _, configuration := range consumers {
		count += configuration.concurrency
	}
	return count
}

// wrapChannelCloseError normaliza los errores devueltos al cerrar un canal AMQP.
func wrapChannelCloseError(err error) error {
	if err == nil || errors.Is(err, amqp.ErrClosed) {
		return nil
	}
	return fmt.Errorf("error cerrando canal rabbitmq: %w", err)
}
