package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	acamole "github.com/darioalvarezma90/go-acamole/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	uri := os.Getenv("RABBITMQ_URL")
	if uri == "" {
		log.Print("set RABBITMQ_URL to run this example")
		return
	}
	server, err := acamole.NewServer(uri, acamole.WithEventHandler(func(event acamole.Event) {
		log.Printf("rabbitmq event=%s queue=%s error=%v", event.Type, event.Queue, event.Err)
	}))
	if err != nil {
		log.Fatal(err)
	}
	if err := server.RegisterConsumer("events", func(_ context.Context, delivery amqp.Delivery) error {
		log.Printf("received %q", delivery.Body)
		return nil
	}); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown RabbitMQ: %v", err)
	}
}
