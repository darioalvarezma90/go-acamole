package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	// ErrRepoUnavailable indica que el wrapper no contiene una colección válida.
	ErrRepoUnavailable = errors.New("repositorio mongodb no disponible")

	// ErrInvalidDocument indica que un filtro o documento no utiliza un tipo BSON
	// soportado por Repository.
	ErrInvalidDocument = errors.New("documento bson inválido")
)

// Repository administra el acceso básico a una colección MongoDB.
//
// Su estado es inmutable después de NewRepository y mongo.Collection es segura
// para uso concurrente. Repository no posee conexiones ni debe cerrar el Client
// del que se obtuvo la colección.
type Repository struct {
	client *Client
	driver *mongo.Collection
}

// NewRepository construye un repositorio sobre una colección de la base
// configurada en Client. El cliente debe permanecer abierto mientras se utilice
// Repository.
func NewRepository(client *Client, collectionName string) (*Repository, error) {
	if client == nil || client.driver == nil || client.database == nil {
		return nil, ErrClientUnavailable
	}
	if client.closed.Load() {
		return nil, ErrClientClosed
	}
	if strings.TrimSpace(collectionName) == "" {
		return nil, fmt.Errorf("error de configuracion del repositorio mongodb: nombre de colección no puede estar vacío")
	}
	if strings.TrimSpace(collectionName) != collectionName {
		return nil, fmt.Errorf("error de configuracion del repositorio mongodb: nombre de colección no puede contener espacios al inicio o al final")
	}

	return &Repository{
		client: client,
		driver: client.database.Collection(collectionName),
	}, nil
}

// Driver devuelve la colección oficial. Repository no ofrece Close porque el
// ciclo de vida de la colección pertenece al Client utilizado durante su
// construcción.
func (r *Repository) Driver() *mongo.Collection {
	if r == nil {
		return nil
	}
	return r.driver
}

// Find devuelve copias independientes de los documentos que coinciden con el
// filtro. filter debe ser bson.M, bson.D o bson.Raw.
func (r *Repository) Find(
	ctx context.Context,
	filter any,
	opts ...options.Lister[options.FindOptions],
) ([]bson.Raw, error) {
	collection, err := r.collectionForOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateBSONDocument(filter, "filtro"); err != nil {
		return nil, err
	}

	cursor, err := collection.Find(ctx, filter, opts...)
	if err != nil {
		return nil, r.wrapOperationError("find", err)
	}
	defer func() {
		_ = cursor.Close(ctx)
	}()

	documents := make([]bson.Raw, 0)
	for cursor.Next(ctx) {
		// Current puede ser reutilizado por el cursor en la siguiente iteración.
		documents = append(documents, append(bson.Raw(nil), cursor.Current...))
	}
	if err := cursor.Err(); err != nil {
		return nil, r.wrapOperationError("find", err)
	}

	return documents, nil
}

// FindOne devuelve una copia independiente del documento que coincide con el
// filtro. El error conserva mongo.ErrNoDocuments cuando no existe coincidencia.
func (r *Repository) FindOne(
	ctx context.Context,
	filter any,
	opts ...options.Lister[options.FindOneOptions],
) (bson.Raw, error) {
	collection, err := r.collectionForOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateBSONDocument(filter, "filtro"); err != nil {
		return nil, err
	}

	document, err := collection.FindOne(ctx, filter, opts...).Raw()
	if err != nil {
		return nil, r.wrapOperationError("find one", err)
	}
	return append(bson.Raw(nil), document...), nil
}

// FindByID busca un documento por su campo _id de tipo bson.ObjectID.
func (r *Repository) FindByID(
	ctx context.Context,
	id bson.ObjectID,
	opts ...options.Lister[options.FindOneOptions],
) (bson.Raw, error) {
	return r.FindOne(ctx, bson.D{{Key: "_id", Value: id}}, opts...)
}

// Insert inserta varios documentos mediante mongo.Collection.InsertMany.
// documents debe ser []bson.M, []bson.D, []bson.Raw o []any con esos tipos.
func (r *Repository) Insert(
	ctx context.Context,
	documents any,
	opts ...options.Lister[options.InsertManyOptions],
) (*mongo.InsertManyResult, error) {
	collection, err := r.collectionForOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateBSONDocuments(documents); err != nil {
		return nil, err
	}

	result, err := collection.InsertMany(ctx, documents, opts...)
	if err != nil {
		return nil, r.wrapOperationError("insert", err)
	}
	return result, nil
}

// InsertOne inserta un documento bson.M, bson.D o bson.Raw.
func (r *Repository) InsertOne(
	ctx context.Context,
	document any,
	opts ...options.Lister[options.InsertOneOptions],
) (*mongo.InsertOneResult, error) {
	collection, err := r.collectionForOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateBSONDocument(document, "documento"); err != nil {
		return nil, err
	}

	result, err := collection.InsertOne(ctx, document, opts...)
	if err != nil {
		return nil, r.wrapOperationError("insert one", err)
	}
	return result, nil
}

// collectionForOperation valida el contexto y devuelve la colección disponible.
func (r *Repository) collectionForOperation(ctx context.Context) (*mongo.Collection, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if r == nil || r.client == nil || r.driver == nil {
		return nil, ErrRepoUnavailable
	}
	if r.client.closed.Load() {
		return nil, ErrClientClosed
	}
	return r.driver, nil
}

// wrapOperationError agrega contexto a un error y conserva sus causas conocidas.
func (r *Repository) wrapOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}

	collectionName := ""
	if r != nil && r.driver != nil {
		collectionName = r.driver.Name()
	}
	operationErr := fmt.Errorf("%s en colección %q: %w", operation, collectionName, err)
	if r != nil && r.client != nil && r.client.closed.Load() {
		return errors.Join(ErrClientClosed, operationErr)
	}
	return operationErr
}

// validateBSONDocument comprueba que un valor sea un documento BSON admitido.
func validateBSONDocument(document any, argumentName string) error {
	switch value := document.(type) {
	case bson.M:
		if value == nil {
			return fmt.Errorf("%w: %s bson.M no puede ser nil", ErrInvalidDocument, argumentName)
		}
	case bson.D:
		if value == nil {
			return fmt.Errorf("%w: %s bson.D no puede ser nil", ErrInvalidDocument, argumentName)
		}
	case bson.Raw:
		if len(value) == 0 {
			return fmt.Errorf("%w: %s bson.Raw no puede estar vacío", ErrInvalidDocument, argumentName)
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("%w: %s contiene bson.Raw inválido: %v", ErrInvalidDocument, argumentName, err)
		}
	default:
		return fmt.Errorf(
			"%w: %s debe ser bson.M, bson.D o bson.Raw; recibido %T",
			ErrInvalidDocument,
			argumentName,
			document,
		)
	}
	return nil
}

// validateBSONDocuments comprueba una colección de documentos BSON admitidos.
func validateBSONDocuments(documents any) error {
	switch values := documents.(type) {
	case []bson.M:
		if len(values) == 0 {
			return emptyDocumentsError()
		}
		for index, document := range values {
			if err := validateBSONDocument(document, fmt.Sprintf("documento en posición %d", index)); err != nil {
				return err
			}
		}
	case []bson.D:
		if len(values) == 0 {
			return emptyDocumentsError()
		}
		for index, document := range values {
			if err := validateBSONDocument(document, fmt.Sprintf("documento en posición %d", index)); err != nil {
				return err
			}
		}
	case []bson.Raw:
		if len(values) == 0 {
			return emptyDocumentsError()
		}
		for index, document := range values {
			if err := validateBSONDocument(document, fmt.Sprintf("documento en posición %d", index)); err != nil {
				return err
			}
		}
	case []any:
		if len(values) == 0 {
			return emptyDocumentsError()
		}
		for index, document := range values {
			if err := validateBSONDocument(document, fmt.Sprintf("documento en posición %d", index)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf(
			"%w: documentos debe ser []bson.M, []bson.D, []bson.Raw o []any; recibido %T",
			ErrInvalidDocument,
			documents,
		)
	}
	return nil
}

// emptyDocumentsError construye el error utilizado para una inserción vacía.
func emptyDocumentsError() error {
	return fmt.Errorf("%w: documentos no puede estar vacío", ErrInvalidDocument)
}
