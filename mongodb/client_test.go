package mongodb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		uri         string
		database    string
		options     []ClientOption
		wantError   error
		wantInError string
	}{
		{name: "nil context", uri: "mongodb://localhost", database: "db", wantError: ErrNilContext},
		{name: "blank URI", ctx: context.Background(), uri: " ", database: "db", wantInError: "uri"},
		{name: "padded URI", ctx: context.Background(), uri: " mongodb://localhost", database: "db", wantInError: "espacios"},
		{name: "blank database", ctx: context.Background(), uri: "mongodb://localhost", database: " ", wantInError: "base de datos"},
		{name: "padded database", ctx: context.Background(), uri: "mongodb://localhost", database: " db", wantInError: "espacios"},
		{name: "nil TLS", ctx: context.Background(), uri: "mongodb://localhost", database: "db", options: []ClientOption{WithTLS(nil)}, wantInError: "tls"},
		{name: "nil driver option", ctx: context.Background(), uri: "mongodb://localhost", database: "db", options: []ClientOption{WithDriverOptions(nil)}, wantInError: "posicion 0"},
		{name: "URI in driver option", ctx: context.Background(), uri: "mongodb://localhost", database: "db", options: []ClientOption{WithDriverOptions(options.Client().ApplyURI("mongodb://other-host"))}, wantInError: "no puede definir una uri"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.ctx, test.uri, test.database, test.options...)
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

func TestWithDriverOptionsSnapshotsInput(t *testing.T) {
	driverOption := options.Client().SetAppName("original")
	clientOption := WithDriverOptions(driverOption)
	driverOption.SetAppName("modified")

	firstClient := &Client{}
	clientOption(firstClient)
	if len(firstClient.driverOptions) != 1 {
		t.Fatalf("driver options length = %d, want 1", len(firstClient.driverOptions))
	}
	if appName := firstClient.driverOptions[0].AppName; appName == nil || *appName != "original" {
		t.Fatalf("driver app name = %v, want original", appName)
	}

	firstClient.driverOptions[0].SetAppName("first-client")
	secondClient := &Client{}
	clientOption(secondClient)
	if appName := secondClient.driverOptions[0].AppName; appName == nil || *appName != "original" {
		t.Fatalf("reused option app name = %v, want original", appName)
	}
}

func TestNewClientReleasesConstructionOptions(t *testing.T) {
	client, err := NewClient(
		context.Background(),
		"mongodb://localhost",
		"db",
		WithConnectionCheck(false),
		WithAppName("mongodb-test"),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	if client.driverOptions != nil {
		t.Errorf("NewClient() retained construction options: %v", client.driverOptions)
	}
	if client.uri != "" {
		t.Errorf("NewClient() retained URI: %q", client.uri)
	}
}

func TestNilClientMethods(t *testing.T) {
	var client *Client

	if client.Driver() != nil {
		t.Error("nil Client.Driver() != nil")
	}
	if client.Database() != nil {
		t.Error("nil Client.Database() != nil")
	}
	if err := client.Ping(context.Background()); !errors.Is(err, ErrClientUnavailable) {
		t.Errorf("nil Client.Ping() error = %v, want ErrClientUnavailable", err)
	}
	if err := client.Close(context.Background()); err != nil {
		t.Errorf("nil Client.Close() error = %v", err)
	}
}

func TestClientCloseWithoutDriverIsIdempotent(t *testing.T) {
	client := &Client{}

	if err := client.Close(nil); !errors.Is(err, ErrNilContext) {
		t.Fatalf("Client.Close(nil) error = %v, want ErrNilContext", err)
	}
	if client.closed.Load() {
		t.Fatal("Client.Close(nil) marked client as closed")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("second Client.Close() error = %v", err)
	}
	if err := client.Ping(context.Background()); !errors.Is(err, ErrClientUnavailable) {
		t.Errorf("closed Client.Ping() error = %v, want ErrClientUnavailable", err)
	}
}

func TestClientConcurrentCloseWithoutDriver(t *testing.T) {
	client := &Client{}

	const closers = 32
	errorsChannel := make(chan error, closers)
	var waitGroup sync.WaitGroup

	for range closers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			errorsChannel <- client.Close(context.Background())
		}()
	}

	waitGroup.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	}
	if !client.closed.Load() {
		t.Error("Client.Close() did not mark client as closed")
	}
}
