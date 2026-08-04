package rabbitmq

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestServerIntegration(t *testing.T) {
	uri := os.Getenv("RABBITMQ_TEST_URL")
	if uri == "" {
		t.Skip("RABBITMQ_TEST_URL no está configurado")
	}

	server, err := NewServer(uri)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	t.Cleanup(shutdown)

	setupChannel, err := server.Driver().Channel()
	if err != nil {
		t.Fatalf("Connection.Channel() error = %v", err)
	}
	queue, err := setupChannel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("QueueDeclare() error = %v", err)
	}
	if err := setupChannel.Close(); err != nil {
		t.Fatalf("Channel.Close() error = %v", err)
	}

	received := make(chan string, 1)
	if err := server.RegisterConsumer(queue.Name, func(_ context.Context, delivery amqp.Delivery) error {
		received <- string(delivery.Body)
		return nil
	}); err != nil {
		t.Fatalf("RegisterConsumer() error = %v", err)
	}

	serveContext, cancelServe := context.WithCancel(context.Background())
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(serveContext)
	}()

	publisher, err := amqp.Dial(uri)
	if err != nil {
		t.Fatalf("amqp.Dial() publisher error = %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	publishChannel, err := publisher.Channel()
	if err != nil {
		t.Fatalf("publisher.Channel() error = %v", err)
	}
	t.Cleanup(func() { _ = publishChannel.Close() })

	publishContext, cancelPublish := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPublish()
	if err := publishChannel.PublishWithContext(
		publishContext,
		"",
		queue.Name,
		false,
		false,
		amqp.Publishing{ContentType: "text/plain", Body: []byte("hello")},
	); err != nil {
		t.Fatalf("PublishWithContext() error = %v", err)
	}

	select {
	case body := <-received:
		if body != "hello" {
			t.Errorf("received body = %q, want hello", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for RabbitMQ delivery")
	}

	cancelServe()
	select {
	case err := <-serveErrors:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Serve() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop")
	}
}
