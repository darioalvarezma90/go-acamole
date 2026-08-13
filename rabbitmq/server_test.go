package rabbitmq

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestNewServerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		options     []ServerOption
		wantInError string
	}{
		{name: "blank URI", uri: " ", wantInError: "uri"},
		{name: "padded URI", uri: " amqp://localhost", wantInError: "espacios"},
		{name: "invalid URI", uri: "://", wantInError: "uri inválida"},
		{name: "negative heartbeat", uri: "amqp://localhost", options: []ServerOption{WithHeartbeat(-time.Second)}, wantInError: "heartbeat"},
		{name: "nil TLS", uri: "amqps://localhost", options: []ServerOption{WithTLSConfig(nil)}, wantInError: "tls"},
		{name: "TLS over plaintext URI", uri: "amqp://localhost", options: []ServerOption{WithTLSConfig(&tls.Config{})}, wantInError: "amqps"},
		{name: "insecure TLS", uri: "amqps://localhost", options: []ServerOption{WithTLSConfig(&tls.Config{InsecureSkipVerify: true})}, wantInError: "InsecureSkipVerify"},
		{name: "old TLS", uri: "amqps://localhost", options: []ServerOption{WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS11})}, wantInError: "1.2"},
		{name: "blank connection name", uri: "amqp://localhost", options: []ServerOption{WithConnectionName(" ")}, wantInError: "nombre de conexion"},
		{name: "nil topology configurer", uri: "amqp://localhost", options: []ServerOption{WithTopologyConfigurer(nil)}, wantInError: "topology"},
		{name: "nil error handler", uri: "amqp://localhost", options: []ServerOption{WithErrorHandler(nil)}, wantInError: "error handler"},
		{name: "nil event handler", uri: "amqp://localhost", options: []ServerOption{WithEventHandler(nil)}, wantInError: "event handler"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewServer(test.uri, test.options...)
			if err == nil {
				t.Fatal("NewServer() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("NewServer() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}
}

func TestNewConsumerValidationAndDefaults(t *testing.T) {
	handler := func(context.Context, amqp.Delivery) error { return nil }
	consumer, err := newConsumer(
		"orders",
		handler,
		WithConsumerTag("orders-worker"),
		WithConsumerConcurrency(3),
		WithPrefetch(8, 1024, false),
		WithRequeueOnError(false),
	)
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	if consumer.workerTag(0) != "orders-worker-1" || consumer.workerTag(2) != "orders-worker-3" {
		t.Errorf("worker tags = %q, %q", consumer.workerTag(0), consumer.workerTag(2))
	}
	if consumer.prefetchCount != 8 || consumer.prefetchSize != 1024 {
		t.Errorf("prefetch = (%d, %d), want (8, 1024)", consumer.prefetchCount, consumer.prefetchSize)
	}
	if consumer.requeueOnError {
		t.Error("requeueOnError = true, want false")
	}

	invalid := []struct {
		name    string
		queue   string
		handler Handler
		opts    []ConsumerOption
	}{
		{name: "blank queue", queue: " ", handler: handler},
		{name: "padded queue", queue: " orders", handler: handler},
		{name: "nil handler", queue: "orders"},
		{name: "zero concurrency", queue: "orders", handler: handler, opts: []ConsumerOption{WithConsumerConcurrency(0)}},
		{name: "exclusive with concurrency", queue: "orders", handler: handler, opts: []ConsumerOption{WithExclusive(true), WithConsumerConcurrency(2)}},
		{name: "negative prefetch", queue: "orders", handler: handler, opts: []ConsumerOption{WithPrefetch(-1, 0, false)}},
		{name: "padded tag", queue: "orders", handler: handler, opts: []ConsumerOption{WithConsumerTag(" worker")}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newConsumer(test.queue, test.handler, test.opts...); err == nil {
				t.Fatal("newConsumer() error = nil")
			}
		})
	}
}

func TestExclusiveConsumerConflictsOnSameQueue(t *testing.T) {
	handler := func(context.Context, amqp.Delivery) error { return nil }
	exclusive, err := newConsumer("orders", handler, WithExclusive(true))
	if err != nil {
		t.Fatalf("newConsumer(exclusive) error = %v", err)
	}
	regular, err := newConsumer("orders", handler)
	if err != nil {
		t.Fatalf("newConsumer(regular) error = %v", err)
	}
	otherQueue, err := newConsumer("payments", handler, WithExclusive(true))
	if err != nil {
		t.Fatalf("newConsumer(other queue) error = %v", err)
	}

	if err := validateConsumerCompatibility([]*consumer{exclusive}, regular); err == nil {
		t.Fatal("regular consumer did not conflict with existing exclusive consumer")
	}
	if err := validateConsumerCompatibility([]*consumer{regular}, exclusive); err == nil {
		t.Fatal("exclusive consumer did not conflict with existing regular consumer")
	}
	if err := validateConsumerCompatibility([]*consumer{exclusive}, otherQueue); err != nil {
		t.Fatalf("consumer on another queue error = %v", err)
	}
}

func TestConsumerArgumentsAreCopied(t *testing.T) {
	arguments := amqp.Table{
		"headers": amqp.Table{
			"trace": []byte("original"),
		},
	}
	option := WithConsumerArguments(arguments)
	arguments["headers"].(amqp.Table)["trace"].([]byte)[0] = 'X'
	arguments["new"] = "value"

	configuration, err := newConsumer(
		"orders",
		func(context.Context, amqp.Delivery) error { return nil },
		option,
	)
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	trace := configuration.arguments["headers"].(amqp.Table)["trace"].([]byte)
	if string(trace) != "original" {
		t.Errorf("copied trace = %q, want original", trace)
	}
	if _, exists := configuration.arguments["new"]; exists {
		t.Error("copied arguments include mutation made after option creation")
	}
}

func TestProcessDeliveryAcknowledgesSuccess(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	configuration, err := newConsumer(
		"orders",
		func(context.Context, amqp.Delivery) error { return nil },
	)
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	server := &Server{errorHandler: func(error) {}}

	err = server.processDelivery(context.Background(), configuration, amqp.Delivery{
		Acknowledger: acknowledger,
		DeliveryTag:  7,
	})
	if err != nil {
		t.Fatalf("processDelivery() error = %v", err)
	}
	if acknowledger.ackTag != 7 || acknowledger.ackCalls != 1 {
		t.Errorf("Ack calls = %d tag = %d, want one call for tag 7", acknowledger.ackCalls, acknowledger.ackTag)
	}
	if acknowledger.nackCalls != 0 {
		t.Errorf("Nack calls = %d, want 0", acknowledger.nackCalls)
	}
}

func TestProcessDeliveryNacksAndReportsHandlerErrors(t *testing.T) {
	wantHandlerError := errors.New("processing failed")
	acknowledger := &recordingAcknowledger{}
	reported := make(chan error, 1)
	server := &Server{errorHandler: func(err error) {
		if calls, _, _ := acknowledger.nackState(); calls != 1 {
			t.Errorf("error handler observed %d Nack calls, want 1", calls)
		}
		reported <- err
	}}
	configuration, err := newConsumer(
		"orders",
		func(context.Context, amqp.Delivery) error { return wantHandlerError },
		WithRequeueOnError(false),
	)
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	delivery := amqp.Delivery{
		Acknowledger: acknowledger,
		ConsumerTag:  "orders-1",
		DeliveryTag:  9,
	}

	if err := server.processDelivery(context.Background(), configuration, delivery); err != nil {
		t.Fatalf("processDelivery() error = %v", err)
	}
	if acknowledger.nackCalls != 1 || acknowledger.nackTag != 9 || acknowledger.requeue {
		t.Errorf("Nack = calls %d tag %d requeue %t", acknowledger.nackCalls, acknowledger.nackTag, acknowledger.requeue)
	}
	select {
	case err := <-reported:
		var handlerError *HandlerError
		if !errors.As(err, &handlerError) || !errors.Is(err, wantHandlerError) {
			t.Fatalf("reported error = %v, want HandlerError wrapping handler error", err)
		}
		if handlerError.Queue != "orders" || handlerError.ConsumerTag != "orders-1" || handlerError.DeliveryTag != 9 {
			t.Errorf("HandlerError = %+v", handlerError)
		}
	default:
		t.Fatal("error handler was not called")
	}
}

func TestCloneAMQPConfigCopiesRecoveryConfiguration(t *testing.T) {
	original := amqp.Config{
		Recovery: &amqp.Recovery{
			ReconnectionConfig: &amqp.ReconnectionConfig{
				MaxRetryCount: 3,
				RetryInterval: time.Second,
			},
		},
	}
	copied := cloneAMQPConfig(original)
	original.Recovery.ReconnectionConfig.MaxRetryCount = 99
	original.Recovery.TopologyRecoveryMode = amqp.TopologyRecoveryDisabled

	if copied.Recovery == original.Recovery {
		t.Fatal("cloneAMQPConfig() reused Recovery pointer")
	}
	if copied.Recovery.ReconnectionConfig == original.Recovery.ReconnectionConfig {
		t.Fatal("cloneAMQPConfig() reused ReconnectionConfig pointer")
	}
	if copied.Recovery.ReconnectionConfig.MaxRetryCount != 3 {
		t.Errorf("copied MaxRetryCount = %d, want 3", copied.Recovery.ReconnectionConfig.MaxRetryCount)
	}
	if copied.Recovery.TopologyRecoveryMode != amqp.TopologyRecoveryAllEnabled {
		t.Errorf("copied TopologyRecoveryMode = %d, want default", copied.Recovery.TopologyRecoveryMode)
	}
}

func TestConnectionCloseDeadlineHonorsCancellationWithoutDeadline(t *testing.T) {
	if _, ok := connectionCloseDeadline(context.Background()); ok {
		t.Fatal("connectionCloseDeadline(Background) returned a deadline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := time.Now()
	deadline, ok := connectionCloseDeadline(ctx)
	if !ok {
		t.Fatal("connectionCloseDeadline(canceled) returned no deadline")
	}
	if deadline.Before(before) || deadline.After(time.Now().Add(time.Second)) {
		t.Errorf("canceled close deadline = %s, want immediate deadline", deadline)
	}
}

func TestConcurrentConnectionCloseRespectsEachCallerDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := &Server{
		closeDone: make(chan struct{}),
		closeDriver: func(time.Time, bool) error {
			close(started)
			<-release
			return nil
		},
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- server.closeConnection(context.Background())
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first close did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	before := time.Now()
	if err := server.closeConnection(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second close error = %v, want context deadline", err)
	}
	if elapsed := time.Since(before); elapsed > time.Second {
		t.Fatalf("second close exceeded its deadline by blocking for %s", elapsed)
	}

	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first close error = %v", err)
	}
	if err := server.closeConnection(context.Background()); err != nil {
		t.Fatalf("cached close error = %v", err)
	}
}

func TestEventHandlerReceivesEventsAndRecoversPanic(t *testing.T) {
	received := make(chan Event, 1)
	server := &Server{eventHandler: func(event Event) { received <- event }}
	want := Event{Type: EventConsumerStarted, Queue: "orders", ConsumerTag: "worker-1"}
	server.reportEvent(want)
	if got := <-received; got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}

	server.eventHandler = func(Event) { panic("observer failed") }
	server.reportEvent(Event{Type: EventServerStopped})

	var nilServer *Server
	nilServer.reportEvent(Event{Type: EventServerStarted})
}

func TestProcessDeliveryReturnsAcknowledgementErrors(t *testing.T) {
	want := errors.New("ack failed")
	acknowledger := &recordingAcknowledger{err: want}
	server := &Server{errorHandler: func(error) {}}
	success, err := newConsumer("orders", func(context.Context, amqp.Delivery) error { return nil })
	if err != nil {
		t.Fatalf("newConsumer(success) error = %v", err)
	}
	if err := server.processDelivery(context.Background(), success, amqp.Delivery{Acknowledger: acknowledger}); !errors.Is(err, want) {
		t.Fatalf("Ack error = %v, want %v", err, want)
	}

	failure, err := newConsumer("orders", func(context.Context, amqp.Delivery) error { return errors.New("handler failed") })
	if err != nil {
		t.Fatalf("newConsumer(failure) error = %v", err)
	}
	if err := server.processDelivery(context.Background(), failure, amqp.Delivery{Acknowledger: acknowledger}); !errors.Is(err, want) {
		t.Fatalf("Nack error = %v, want %v", err, want)
	}
}

func TestServeStartsConsumersAndStopsOnContext(t *testing.T) {
	channel := newFakeConsumerChannel()
	events := make(chan Event, 8)
	server := newTestRabbitMQServer(channel)
	server.eventHandler = func(event Event) { events <- event }
	configuration, err := newConsumer("orders", func(context.Context, amqp.Delivery) error { return nil })
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	server.consumers = []*consumer{configuration}

	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(ctx) }()
	waitForRabbitMQEvent(t, events, EventServerStarted)
	cancel()
	if err := <-serveResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve() error = %v, want context.Canceled", err)
	}
	if channel.closeCount() != 1 {
		t.Fatalf("consumer channel close count = %d, want 1", channel.closeCount())
	}
	waitForRabbitMQEvent(t, events, EventServerStopped)
}

func TestServeCleansUpAfterPartialWorkerStartup(t *testing.T) {
	first := newFakeConsumerChannel()
	want := errors.New("second channel failed")
	server := newTestRabbitMQServer(first)
	calls := 0
	server.openConsumerChannel = func() (consumerChannel, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return nil, want
	}
	configuration, err := newConsumer(
		"orders",
		func(context.Context, amqp.Delivery) error { return nil },
		WithConsumerConcurrency(2),
	)
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	server.consumers = []*consumer{configuration}

	if err := server.Serve(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Serve() error = %v, want partial-start error", err)
	}
	if first.closeCount() != 1 {
		t.Fatalf("first channel close count = %d, want 1", first.closeCount())
	}
}

func TestServeReportsQosAndConsumeFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeConsumerChannel, error)
		want      error
	}{
		{
			name: "qos",
			configure: func(channel *fakeConsumerChannel, want error) {
				channel.qosErr = want
			},
			want: errors.New("qos failed"),
		},
		{
			name: "consume",
			configure: func(channel *fakeConsumerChannel, want error) {
				channel.consumeErr = want
			},
			want: errors.New("consume failed"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := newFakeConsumerChannel()
			test.configure(channel, test.want)
			server := newTestRabbitMQServer(channel)
			configuration, err := newConsumer("orders", func(context.Context, amqp.Delivery) error { return nil })
			if err != nil {
				t.Fatalf("newConsumer() error = %v", err)
			}
			server.consumers = []*consumer{configuration}
			if err := server.Serve(context.Background()); !errors.Is(err, test.want) {
				t.Fatalf("Serve() error = %v, want %v", err, test.want)
			}
			if channel.closeCount() != 1 {
				t.Fatalf("channel close count = %d, want 1", channel.closeCount())
			}
		})
	}
}

func TestServeDetectsUnexpectedDeliveryClosure(t *testing.T) {
	channel := newFakeConsumerChannel()
	server := newTestRabbitMQServer(channel)
	configuration, err := newConsumer("orders", func(context.Context, amqp.Delivery) error { return nil })
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	server.consumers = []*consumer{configuration}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(context.Background()) }()
	select {
	case <-channel.consumeStarted:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}
	channel.closeDeliveries()
	select {
	case err := <-serveResult:
		if err == nil || !strings.Contains(err.Error(), "inesperadamente") {
			t.Fatalf("Serve() error = %v, want unexpected-closure error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not detect closed deliveries")
	}
}

func TestProcessDeliveryRecoversHandlerPanic(t *testing.T) {
	var reported error
	server := &Server{errorHandler: func(err error) { reported = err }}
	configuration, err := newConsumer(
		"orders",
		func(context.Context, amqp.Delivery) error { panic("boom") },
	)
	if err != nil {
		t.Fatalf("newConsumer() error = %v", err)
	}
	acknowledger := &recordingAcknowledger{}

	if err := server.processDelivery(context.Background(), configuration, amqp.Delivery{Acknowledger: acknowledger}); err != nil {
		t.Fatalf("processDelivery() error = %v", err)
	}
	if acknowledger.nackCalls != 1 || !acknowledger.requeue {
		t.Errorf("panic Nack = calls %d requeue %t, want one requeue", acknowledger.nackCalls, acknowledger.requeue)
	}
	if reported == nil || !strings.Contains(reported.Error(), "panic en handler") {
		t.Fatalf("reported error = %v, want recovered panic", reported)
	}
}

func TestNilServerMethods(t *testing.T) {
	var server *Server

	if server.Driver() != nil {
		t.Error("nil Server.Driver() != nil")
	}
	if err := server.RegisterConsumer("orders", func(context.Context, amqp.Delivery) error { return nil }); !errors.Is(err, ErrServerUnavailable) {
		t.Errorf("nil Server.RegisterConsumer() error = %v, want ErrServerUnavailable", err)
	}
	if err := server.Serve(context.Background()); !errors.Is(err, ErrServerUnavailable) {
		t.Errorf("nil Server.Serve() error = %v, want ErrServerUnavailable", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Errorf("nil Server.Shutdown() error = %v", err)
	}
}

func TestShutdownWithoutDriverIsIdempotentAndConcurrent(t *testing.T) {
	server := &Server{}
	if err := server.Shutdown(nil); !errors.Is(err, ErrNilContext) {
		t.Fatalf("Shutdown(nil) error = %v, want ErrNilContext", err)
	}

	const callers = 32
	errorsChannel := make(chan error, callers)
	var waitGroup sync.WaitGroup
	for range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			errorsChannel <- server.Shutdown(context.Background())
		}()
	}
	waitGroup.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}
	if !server.closed.Load() {
		t.Error("Shutdown() did not mark server as closed")
	}
}

type recordingAcknowledger struct {
	mutex     sync.Mutex
	ackCalls  int
	ackTag    uint64
	nackCalls int
	nackTag   uint64
	requeue   bool
	err       error
}

type fakeConsumerChannel struct {
	mutex          sync.Mutex
	deliveries     chan amqp.Delivery
	consumeStarted chan struct{}
	consumeOnce    sync.Once
	closeOnce      sync.Once
	qosErr         error
	consumeErr     error
	closes         int
}

func newFakeConsumerChannel() *fakeConsumerChannel {
	return &fakeConsumerChannel{
		deliveries:     make(chan amqp.Delivery),
		consumeStarted: make(chan struct{}),
	}
}

func (c *fakeConsumerChannel) Qos(int, int, bool) error {
	return c.qosErr
}

func (c *fakeConsumerChannel) ConsumeWithContext(
	ctx context.Context,
	_ string,
	_ string,
	_ bool,
	_ bool,
	_ bool,
	_ bool,
	_ amqp.Table,
) (<-chan amqp.Delivery, error) {
	if c.consumeErr != nil {
		return nil, c.consumeErr
	}
	c.consumeOnce.Do(func() { close(c.consumeStarted) })
	go func() {
		<-ctx.Done()
		c.closeDeliveries()
	}()
	return c.deliveries, nil
}

func (c *fakeConsumerChannel) Close() error {
	c.mutex.Lock()
	c.closes++
	c.mutex.Unlock()
	c.closeDeliveries()
	return nil
}

func (c *fakeConsumerChannel) closeDeliveries() {
	c.closeOnce.Do(func() { close(c.deliveries) })
}

func (c *fakeConsumerChannel) closeCount() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.closes
}

func newTestRabbitMQServer(channel consumerChannel) *Server {
	connectionClosed := make(chan *amqp.Error)
	return &Server{
		driver:       &amqp.Connection{},
		errorHandler: func(error) {},
		eventHandler: func(Event) {},
		done:         make(chan struct{}),
		closeDone:    make(chan struct{}),
		openConsumerChannel: func() (consumerChannel, error) {
			return channel, nil
		},
		notifyConnectionClose: func(chan *amqp.Error) <-chan *amqp.Error {
			return connectionClosed
		},
		closeDriver: func(time.Time, bool) error { return nil },
	}
}

func waitForRabbitMQEvent(t *testing.T, events <-chan Event, eventType EventType) Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == eventType {
				return event
			}
		case <-deadline:
			t.Fatalf("event %q was not received", eventType)
		}
	}
}

func (a *recordingAcknowledger) Ack(tag uint64, _ bool) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.ackCalls++
	a.ackTag = tag
	return a.err
}

func (a *recordingAcknowledger) Nack(tag uint64, _ bool, requeue bool) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.nackCalls++
	a.nackTag = tag
	a.requeue = requeue
	return a.err
}

func (a *recordingAcknowledger) Reject(uint64, bool) error {
	return a.err
}

func (a *recordingAcknowledger) nackState() (calls int, tag uint64, requeue bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.nackCalls, a.nackTag, a.requeue
}
