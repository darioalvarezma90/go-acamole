package grpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestNewServerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		options     []ServerOption
		wantInError string
	}{
		{name: "blank address", address: " ", wantInError: "direccion"},
		{name: "padded address", address: " 127.0.0.1:0", wantInError: "espacios"},
		{name: "blank network", address: "127.0.0.1:0", options: []ServerOption{WithNetwork(" ")}, wantInError: "red"},
		{name: "padded network", address: "127.0.0.1:0", options: []ServerOption{WithNetwork(" tcp")}, wantInError: "espacios"},
		{name: "nil grpc option", address: "127.0.0.1:0", options: []ServerOption{WithGRPCOptions(nil)}, wantInError: "posicion 0"},
		{name: "nil unary interceptor", address: "127.0.0.1:0", options: []ServerOption{WithUnaryInterceptors(nil)}, wantInError: "interceptor unary"},
		{name: "duplicate unary interceptor", address: "127.0.0.1:0", options: []ServerOption{WithGRPCOptions(grpcgo.UnaryInterceptor(passthroughUnaryInterceptor), grpcgo.UnaryInterceptor(passthroughUnaryInterceptor))}, wantInError: "aplicando opciones"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewServer(test.address, test.options...)
			if err == nil {
				t.Fatal("NewServer() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("NewServer() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}
}

func TestNilServerMethods(t *testing.T) {
	var server *Server

	if server.Driver() != nil {
		t.Error("nil Server.Driver() != nil")
	}
	if server.Address() != "" {
		t.Errorf("nil Server.Address() = %q", server.Address())
	}
	if err := server.ListenAndServe(); !errors.Is(err, ErrServerUnavailable) {
		t.Errorf("nil Server.ListenAndServe() error = %v, want ErrServerUnavailable", err)
	}
	if err := server.Serve(nil); !errors.Is(err, ErrNilListener) {
		t.Errorf("nil Server.Serve(nil) error = %v, want ErrNilListener", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Errorf("nil Server.Shutdown() error = %v", err)
	}
}

func TestServerServesHealthAndShutsDown(t *testing.T) {
	server, err := NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	healthpb.RegisterHealthServer(server.Driver(), healthyServer{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	wantAddress := listener.Addr().String()
	addressDeadline := time.Now().Add(5 * time.Second)
	for server.Address() != wantAddress && time.Now().Before(addressDeadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Address() != wantAddress {
		t.Errorf("Address() = %q, want %q", server.Address(), wantAddress)
	}
	client, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	checkContext, cancelCheck := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCheck()
	response, err := healthpb.NewHealthClient(client).Check(checkContext, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health.Check() error = %v", err)
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("health status = %s, want SERVING", response.Status)
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-serveErrors:
		if err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop")
	}
	if err := server.Serve(listener); !errors.Is(err, ErrServerClosed) {
		t.Errorf("Serve() after Shutdown error = %v, want ErrServerClosed", err)
	}
}

func TestShutdownIsConcurrentAndIdempotent(t *testing.T) {
	server, err := NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	waitForServerAddress(t, server, listener.Addr().String())

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
	select {
	case err := <-serveErrors:
		if err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop")
	}
}

func TestShutdownForcesStopAfterDeadline(t *testing.T) {
	handler := &blockingHealthServer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	server, err := NewServer(
		"127.0.0.1:0",
		WithGRPCOptions(grpcgo.WaitForHandlers(true)),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	healthpb.RegisterHealthServer(server.Driver(), handler)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	client, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	callErrors := make(chan error, 1)
	go func() {
		_, err := healthpb.NewHealthClient(client).Check(context.Background(), &healthpb.HealthCheckRequest{})
		callErrors <- err
	}()

	select {
	case <-handler.started:
	case <-time.After(5 * time.Second):
		t.Fatal("health handler did not start")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShutdown()
	shutdownStarted := time.Now()
	if err := server.Shutdown(shutdownContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(shutdownStarted); elapsed > time.Second {
		t.Fatalf("Shutdown() blocked for %s after its deadline", elapsed)
	}
	select {
	case <-server.shutdownDone:
		t.Fatal("shutdown completed while the configured handler was still blocked")
	default:
	}
	close(handler.release)
	select {
	case <-callErrors:
	case <-time.After(5 * time.Second):
		t.Fatal("client RPC did not finish after forced Stop")
	}
	select {
	case err := <-serveErrors:
		if err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop after deadline")
	}
	completionContext, cancelCompletion := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCompletion()
	if err := server.Shutdown(completionContext); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}

type healthyServer struct {
	healthpb.UnimplementedHealthServer
}

func passthroughUnaryInterceptor(
	ctx context.Context,
	request any,
	_ *grpcgo.UnaryServerInfo,
	handler grpcgo.UnaryHandler,
) (any, error) {
	return handler(ctx, request)
}

func (healthyServer) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

type blockingHealthServer struct {
	healthpb.UnimplementedHealthServer
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func waitForServerAddress(t *testing.T, server *Server, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for server.Address() != address && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Address() != address {
		t.Fatalf("server did not start on %q; Address() = %q", address, server.Address())
	}
}

func (s *blockingHealthServer) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}
