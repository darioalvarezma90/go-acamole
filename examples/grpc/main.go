package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	acamole "github.com/darioalvarezma90/go-acamole/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	server, err := acamole.NewServer("127.0.0.1:8080")
	if err != nil {
		log.Fatal(err)
	}
	healthpb.RegisterHealthServer(server.Driver(), health.NewServer())
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.ListenAndServe() }()

	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, acamole.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-signals.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown gRPC: %v", err)
		}
	}
}
