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
		{name: "invalid URI", uri: "://", wantInError: "uri inválida"},
		{name: "negative heartbeat", uri: "amqp://localhost", options: []ServerOption{WithHeartbeat(-time.Second)}, wantInError: "heartbeat"},
		{name: "nil TLS", uri: "amqps://localhost", options: []ServerOption{WithTLSConfig(nil)}, wantInError: "tls"},
		{name: "TLS over plaintext URI", uri: "amqp://localhost", options: []ServerOption{WithTLSConfig(&tls.Config{})}, wantInError: "amqps"},
		{name: "insecure TLS", uri: "amqps://localhost", options: []ServerOption{WithTLSConfig(&tls.Config{InsecureSkipVerify: true})}, wantInError: "InsecureSkipVerify"},
		{name: "old TLS", uri: "amqps://localhost", options: []ServerOption{WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS11})}, wantInError: "1.2"},
		{name: "blank connection name", uri: "amqp://localhost", options: []ServerOption{WithConnectionName(" ")}, wantInError: "nombre de conexion"},
		{name: "nil topology configurer", uri: "amqp://localhost", options: []ServerOption{WithTopologyConfigurer(nil)}, wantInError: "topology"},
		{name: "nil error handler", uri: "amqp://localhost", options: []ServerOption{WithErrorHandler(nil)}, wantInError: "error handler"},
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
