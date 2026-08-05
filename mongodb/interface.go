package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// IClient define el contrato común de un cliente MongoDB.
//
// El wrapper administra el ciclo de vida de la conexión, pero permite utilizar
// directamente los tipos del driver oficial para no duplicar su API.
type IClient interface {
	// Driver devuelve el cliente nativo del driver oficial.
	Driver() *mongo.Client

	// Database devuelve la base de datos configurada durante la construcción.
	Database() *mongo.Database

	// Ping verifica que el deployment configurado sea alcanzable.
	Ping(ctx context.Context) error

	// Close desconecta el cliente y libera sus recursos.
	Close(ctx context.Context) error
}

// Valida en tiempo de compilación que Client implementa IClient.
var _ IClient = (*Client)(nil)

// IRepository define el contrato común de una colección MongoDB.
//
// El repositorio trabaja con documentos BSON nativos y conserva el acceso a la
// colección oficial para operaciones que no formen parte de este contrato
// básico. Su ciclo de vida pertenece al Client utilizado para construirlo.
type IRepository interface {
	// Driver devuelve la colección nativa del driver oficial.
	Driver() *mongo.Collection

	// Find devuelve todos los documentos que coinciden con el filtro.
	Find(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) ([]bson.Raw, error)

	// FindOne devuelve un documento que coincide con el filtro.
	FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) (bson.Raw, error)

	// FindByID busca un documento mediante un ObjectID nativo BSON.
	FindByID(ctx context.Context, id bson.ObjectID, opts ...options.Lister[options.FindOneOptions]) (bson.Raw, error)

	// Insert inserta varios documentos BSON mediante mongo.Collection.InsertMany.
	Insert(ctx context.Context, documents any, opts ...options.Lister[options.InsertManyOptions]) (*mongo.InsertManyResult, error)

	// InsertOne inserta un único documento BSON.
	InsertOne(ctx context.Context, document any, opts ...options.Lister[options.InsertOneOptions]) (*mongo.InsertOneResult, error)
}

// Valida en tiempo de compilación que Repository implementa IRepository.
var _ IRepository = (*Repository)(nil)
