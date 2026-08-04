package postgresql

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestClientIntegration(t *testing.T) {
	connectionString := os.Getenv("POSTGRESQL_TEST_DSN")
	if connectionString == "" {
		t.Skip("POSTGRESQL_TEST_DSN no está configurado")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewClient(ctx, connectionString)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(client.Close)

	var value int
	if err := client.Driver().QueryRow(ctx, "select 1").Scan(&value); err != nil {
		t.Fatalf("QueryRow() error = %v", err)
	}
	if value != 1 {
		t.Errorf("select 1 = %d, want 1", value)
	}
}
