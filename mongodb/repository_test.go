package mongodb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNewRepository(t *testing.T) {
	client := newRepositoryTestClient(t)

	repository, err := NewRepository(client, "orders")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	if repository.Driver() == nil {
		t.Fatal("Repository.Driver() = nil")
	}
	if name := repository.Driver().Name(); name != "orders" {
		t.Errorf("Repository.Driver().Name() = %q, want orders", name)
	}
	if repository.client != client {
		t.Error("Repository did not retain its lifecycle client")
	}
}

func TestNewRepositoryRejectsInvalidConfiguration(t *testing.T) {
	client := newRepositoryTestClient(t)

	tests := []struct {
		name        string
		client      *Client
		collection  string
		wantError   error
		wantInError string
	}{
		{name: "nil client", collection: "orders", wantError: ErrClientUnavailable},
		{name: "unavailable client", client: &Client{}, collection: "orders", wantError: ErrClientUnavailable},
		{name: "blank collection", client: client, collection: " ", wantInError: "colección"},
		{name: "padded collection", client: client, collection: " orders ", wantInError: "espacios"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRepository(test.client, test.collection)
			if err == nil {
				t.Fatal("NewRepository() error = nil")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Errorf("NewRepository() error = %v, want %v", err, test.wantError)
			}
			if test.wantInError != "" && !strings.Contains(err.Error(), test.wantInError) {
				t.Errorf("NewRepository() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}

	closedClient := newRepositoryTestClient(t)
	if err := closedClient.Close(context.Background()); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}
	if _, err := NewRepository(closedClient, "orders"); !errors.Is(err, ErrClientClosed) {
		t.Errorf("NewRepository() error = %v, want ErrClientClosed", err)
	}
}

func TestNilRepositoryMethods(t *testing.T) {
	var repository *Repository

	if repository.Driver() != nil {
		t.Error("nil Repository.Driver() != nil")
	}
	if _, err := repository.Find(context.Background(), bson.D{}); !errors.Is(err, ErrRepoUnavailable) {
		t.Errorf("nil Repository.Find() error = %v, want ErrRepoUnavailable", err)
	}
	if _, err := repository.FindOne(context.Background(), bson.D{}); !errors.Is(err, ErrRepoUnavailable) {
		t.Errorf("nil Repository.FindOne() error = %v, want ErrRepoUnavailable", err)
	}
	if _, err := repository.Insert(context.Background(), []bson.D{{}}); !errors.Is(err, ErrRepoUnavailable) {
		t.Errorf("nil Repository.Insert() error = %v, want ErrRepoUnavailable", err)
	}
	if _, err := repository.InsertOne(context.Background(), bson.D{}); !errors.Is(err, ErrRepoUnavailable) {
		t.Errorf("nil Repository.InsertOne() error = %v, want ErrRepoUnavailable", err)
	}
}

func TestRepositoryRejectsNilContextAndClosedClient(t *testing.T) {
	client := newRepositoryTestClient(t)
	repository, err := NewRepository(client, "orders")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	if _, err := repository.Find(nil, bson.D{}); !errors.Is(err, ErrNilContext) {
		t.Errorf("Repository.Find(nil) error = %v, want ErrNilContext", err)
	}
	if _, err := repository.FindOne(nil, bson.D{}); !errors.Is(err, ErrNilContext) {
		t.Errorf("Repository.FindOne(nil) error = %v, want ErrNilContext", err)
	}
	if _, err := repository.Insert(nil, []bson.D{{}}); !errors.Is(err, ErrNilContext) {
		t.Errorf("Repository.Insert(nil) error = %v, want ErrNilContext", err)
	}
	if _, err := repository.InsertOne(nil, bson.D{}); !errors.Is(err, ErrNilContext) {
		t.Errorf("Repository.InsertOne(nil) error = %v, want ErrNilContext", err)
	}

	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}
	if _, err := repository.collectionForOperation(context.Background()); !errors.Is(err, ErrClientClosed) {
		t.Errorf("collectionForOperation() error = %v, want ErrClientClosed", err)
	}
}

func TestValidateBSONDocument(t *testing.T) {
	raw := mustRawDocument(t, bson.D{{Key: "status", Value: "pending"}})

	valid := []any{bson.M{}, bson.D{}, raw}
	for _, document := range valid {
		if err := validateBSONDocument(document, "documento"); err != nil {
			t.Errorf("validateBSONDocument(%T) error = %v", document, err)
		}
	}

	invalid := []any{
		bson.M(nil),
		bson.D(nil),
		bson.Raw(nil),
		bson.Raw{1, 2, 3},
		"not-bson",
	}
	for _, document := range invalid {
		if err := validateBSONDocument(document, "documento"); !errors.Is(err, ErrInvalidDocument) {
			t.Errorf("validateBSONDocument(%T) error = %v, want ErrInvalidDocument", document, err)
		}
	}
}

func TestValidateBSONDocuments(t *testing.T) {
	raw := mustRawDocument(t, bson.M{"status": "pending"})

	valid := []any{
		[]bson.M{{"status": "pending"}},
		[]bson.D{{{Key: "status", Value: "pending"}}},
		[]bson.Raw{raw},
		[]any{bson.M{}, bson.D{}, raw},
	}
	for _, documents := range valid {
		if err := validateBSONDocuments(documents); err != nil {
			t.Errorf("validateBSONDocuments(%T) error = %v", documents, err)
		}
	}

	invalid := []any{
		[]bson.M{},
		[]bson.D{},
		[]bson.Raw{},
		[]any{},
		[]any{"not-bson"},
		[]string{"not-bson"},
	}
	for _, documents := range invalid {
		if err := validateBSONDocuments(documents); !errors.Is(err, ErrInvalidDocument) {
			t.Errorf("validateBSONDocuments(%T) error = %v, want ErrInvalidDocument", documents, err)
		}
	}
}

func TestRepositoryConcurrentLifecycleChecks(t *testing.T) {
	client := newRepositoryTestClient(t)
	repository, err := NewRepository(client, "orders")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	const readers = 64
	start := make(chan struct{})
	errorsChannel := make(chan error, readers)
	var waitGroup sync.WaitGroup

	for range readers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := repository.collectionForOperation(context.Background())
			errorsChannel <- err
		}()
	}

	close(start)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}
	waitGroup.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil && !errors.Is(err, ErrClientClosed) {
			t.Errorf("collectionForOperation() error = %v", err)
		}
	}
	if repository.Driver() == nil {
		t.Error("Repository.Driver() = nil after client close")
	}
}

func TestRepositoryWrapOperationErrorPreservesDriverAndLifecycleErrors(t *testing.T) {
	client := newRepositoryTestClient(t)
	repository, err := NewRepository(client, "orders")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	driverErr := errors.New("driver failure")

	wrapped := repository.wrapOperationError("find", driverErr)
	if !errors.Is(wrapped, driverErr) {
		t.Errorf("wrapOperationError() error = %v, want driver error", wrapped)
	}
	if errors.Is(wrapped, ErrClientClosed) {
		t.Errorf("wrapOperationError() unexpectedly contains ErrClientClosed: %v", wrapped)
	}

	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}
	wrapped = repository.wrapOperationError("find", driverErr)
	if !errors.Is(wrapped, driverErr) || !errors.Is(wrapped, ErrClientClosed) {
		t.Errorf("wrapOperationError() error = %v, want driver and closed errors", wrapped)
	}
}

func newRepositoryTestClient(t *testing.T) *Client {
	t.Helper()

	client, err := NewClient(
		context.Background(),
		"mongodb://localhost",
		"application",
		WithConnectionCheck(false),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})
	return client
}

func mustRawDocument(t *testing.T, document any) bson.Raw {
	t.Helper()

	encoded, err := bson.Marshal(document)
	if err != nil {
		t.Fatalf("bson.Marshal() error = %v", err)
	}
	return bson.Raw(encoded)
}
