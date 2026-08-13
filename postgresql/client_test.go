package postgresql

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const offlineConnectionString = "postgres://test:secret@localhost:5432/testdb?sslmode=disable"

func TestNewClientWithoutConnectionCheck(t *testing.T) {
	client, err := NewClient(
		context.Background(),
		offlineConnectionString,
		WithConnectionCheck(false),
		WithApplicationName("orders-api"),
		WithMaxConnections(12),
		WithMinConnections(0),
		WithMinIdleConnections(0),
		WithConnectTimeout(2*time.Second),
		WithMaxConnectionLifetime(time.Hour),
		WithMaxConnectionLifetimeJitter(time.Minute),
		WithMaxConnectionIdleTime(10*time.Minute),
		WithHealthCheckPeriod(30*time.Second),
		WithPingTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(client.Close)

	if client.Driver() == nil {
		t.Fatal("NewClient() driver = nil")
	}
	configuration := client.Driver().Config()
	if configuration.MaxConns != 12 {
		t.Errorf("MaxConns = %d, want 12", configuration.MaxConns)
	}
	if configuration.ConnConfig.RuntimeParams["application_name"] != "orders-api" {
		t.Errorf("application_name = %q, want orders-api", configuration.ConnConfig.RuntimeParams["application_name"])
	}
	if configuration.ConnConfig.ConnectTimeout != 2*time.Second {
		t.Errorf("ConnectTimeout = %s, want 2s", configuration.ConnConfig.ConnectTimeout)
	}
}

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name             string
		ctx              context.Context
		connectionString string
		options          []ClientOption
		wantError        error
		wantInError      string
	}{
		{name: "nil context", connectionString: offlineConnectionString, wantError: ErrNilContext},
		{name: "blank connection string", ctx: context.Background(), connectionString: " ", wantInError: "cadena de conexion"},
		{name: "padded connection string", ctx: context.Background(), connectionString: " postgres://localhost/db", wantInError: "espacios"},
		{name: "invalid connection string", ctx: context.Background(), connectionString: "postgres://%", wantInError: "interpretando"},
		{name: "nil pool configurer", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithPoolConfigurer(nil)}, wantInError: "configurador"},
		{name: "zero max connections", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithMaxConnections(0)}, wantInError: "mayor que cero"},
		{name: "negative minimum", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithMinConnections(-1)}, wantInError: "no puede ser negativo"},
		{name: "minimum above maximum", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithMaxConnections(2), WithMinConnections(3)}, wantInError: "superar el maximo"},
		{name: "invalid health check period", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithHealthCheckPeriod(0)}, wantInError: "mayor que cero"},
		{name: "blank application name", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithApplicationName(" ")}, wantInError: "aplicacion"},
		{name: "padded application name", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithApplicationName(" service")}, wantInError: "espacios"},
		{name: "nil TLS", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithTLSConfig(nil)}, wantInError: "tls"},
		{name: "required TLS disabled", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithRequireTLS()}, wantInError: "sin tls"},
		{name: "configurer error", ctx: context.Background(), connectionString: offlineConnectionString, options: []ClientOption{WithPoolConfigurer(func(*pgxpool.Config) error { return errors.New("custom failure") })}, wantInError: "custom failure"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.ctx, test.connectionString, test.options...)
			if err == nil {
				t.Fatal("NewClient() error = nil")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("NewClient() error = %v, want %v", err, test.wantError)
			}
			if test.wantInError != "" && !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("NewClient() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}
}

func TestNilClientMethods(t *testing.T) {
	var client *Client

	if client.Driver() != nil {
		t.Error("nil Client.Driver() != nil")
	}
	if err := client.Ping(context.Background()); !errors.Is(err, ErrClientUnavailable) {
		t.Errorf("nil Client.Ping() error = %v, want ErrClientUnavailable", err)
	}
	client.Close()
}

func TestClientCloseIsIdempotentAndConcurrent(t *testing.T) {
	client, err := NewClient(
		context.Background(),
		offlineConnectionString,
		WithConnectionCheck(false),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	const closers = 32
	var waitGroup sync.WaitGroup
	for range closers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			client.Close()
		}()
	}
	waitGroup.Wait()

	if !client.closed.Load() {
		t.Error("Client.Close() did not mark client as closed")
	}
	if err := client.Ping(context.Background()); !errors.Is(err, ErrClientClosed) {
		t.Errorf("closed Client.Ping() error = %v, want ErrClientClosed", err)
	}
}

func TestClientPingRejectsNilContext(t *testing.T) {
	client, err := NewClient(
		context.Background(),
		offlineConnectionString,
		WithConnectionCheck(false),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(client.Close)

	if err := client.Ping(nil); !errors.Is(err, ErrNilContext) {
		t.Errorf("Client.Ping(nil) error = %v, want ErrNilContext", err)
	}
}

func TestNewClientAppliesVerifiedTLS(t *testing.T) {
	client, err := NewClient(
		context.Background(),
		offlineConnectionString,
		WithConnectionCheck(false),
		WithTLSConfig(&tls.Config{}),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(client.Close)

	configuration := client.Driver().Config().ConnConfig
	if configuration.TLSConfig == nil {
		t.Fatal("TLSConfig = nil")
	}
	if configuration.TLSConfig.ServerName != "localhost" {
		t.Errorf("TLS ServerName = %q, want localhost", configuration.TLSConfig.ServerName)
	}
	if configuration.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("TLS MinVersion = %d, want TLS 1.2", configuration.TLSConfig.MinVersion)
	}
	for index, fallback := range configuration.Fallbacks {
		if fallback == nil || fallback.TLSConfig == nil {
			t.Errorf("fallback %d permits plaintext", index)
		}
	}
}

func TestNewClientRejectsInsecureTLS(t *testing.T) {
	_, err := NewClient(
		context.Background(),
		offlineConnectionString,
		WithConnectionCheck(false),
		WithTLSConfig(&tls.Config{InsecureSkipVerify: true}),
	)
	if err == nil {
		t.Fatal("NewClient() error = nil")
	}
	if !strings.Contains(err.Error(), "InsecureSkipVerify") {
		t.Fatalf("NewClient() error = %q, want InsecureSkipVerify message", err)
	}
}
