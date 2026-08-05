package postgresql

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// IClient define el contrato común de un cliente PostgreSQL.
//
// El wrapper administra la construcción, verificación y cierre del pool, pero
// expone el tipo oficial de pgx para no duplicar su API de consultas.
type IClient interface {
	// Driver devuelve el pool nativo de pgx.
	Driver() *pgxpool.Pool

	// Ping verifica que PostgreSQL sea alcanzable.
	Ping(ctx context.Context) error

	// Close cierra el pool y libera todas sus conexiones.
	Close()
}

// Valida en tiempo de compilación que Client implementa IClient.
var _ IClient = (*Client)(nil)
