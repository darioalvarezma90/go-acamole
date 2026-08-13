package mongodb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestClientAndRepositoryIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI no está configurado")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	databaseName := fmt.Sprintf("go_acamole_test_%d", time.Now().UnixNano())
	client, err := NewClient(ctx, uri, databaseName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := client.Close(closeCtx); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	repository, err := NewRepository(client, "documents")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dropCancel()
		if err := client.Database().Drop(dropCtx); err != nil {
			t.Errorf("Database.Drop() error = %v", err)
		}
	})

	inserted, err := repository.InsertOne(ctx, bson.M{"name": "integration"})
	if err != nil {
		t.Fatalf("InsertOne() error = %v", err)
	}
	id, ok := inserted.InsertedID.(bson.ObjectID)
	if !ok {
		t.Fatalf("InsertedID type = %T, want bson.ObjectID", inserted.InsertedID)
	}
	document, err := repository.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	var decoded bson.M
	if err := bson.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("bson.Unmarshal() error = %v", err)
	}
	if decoded["name"] != "integration" {
		t.Fatalf("document name = %v, want integration", decoded["name"])
	}
}
