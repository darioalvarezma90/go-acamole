package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Interface define el contrato común de un cliente MongoDB.
//
// El wrapper administra el ciclo de vida de la conexión, pero permite utilizar
// directamente los tipos del driver oficial para no duplicar su API.
type Interface interface {
	// Driver devuelve el cliente nativo del driver oficial.
	Driver() *mongo.Client

	// Database devuelve la base de datos configurada durante la construcción.
	Database() *mongo.Database

	// Ping verifica que el deployment configurado sea alcanzable.
	Ping(ctx context.Context) error

	// Close desconecta el cliente y libera sus recursos.
	Close(ctx context.Context) error
}

// Valida en tiempo de compilación que Client implementa Interface.
var _ Interface = (*Client)(nil)
