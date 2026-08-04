// Package postgresql proporciona un cliente PostgreSQL reutilizable construido
// sobre el pool de conexiones concurrente de pgx.
package postgresql

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNilContext indica que se recibió un contexto nil.
	ErrNilContext = errors.New("contexto no puede ser nil")

	// ErrClientClosed indica que se intentó usar un cliente ya cerrado.
	ErrClientClosed = errors.New("cliente postgresql cerrado")

	// ErrClientUnavailable indica que el wrapper no contiene un pool válido.
	ErrClientUnavailable = errors.New("cliente postgresql no disponible")
)

// Client administra un pool compartido del driver pgx. Puede reutilizarse de
// forma segura desde múltiples goroutines y no debe crearse por petición.
type Client struct {
	connectionString string
	connectionCheck  bool
	poolConfigurers  []PoolConfigurer

	tlsConfigured bool
	tlsConfig     *tls.Config
	requireTLS    bool

	driver *pgxpool.Pool

	closeMutex sync.Mutex
	closed     atomic.Bool
}

// NewClient construye un pool PostgreSQL y, de forma predeterminada, verifica
// la conectividad mediante Ping. La cadena puede usar formato URL o libpq.
func NewClient(ctx context.Context, connectionString string, opts ...ClientOption) (*Client, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	client := &Client{
		connectionString: connectionString,
		connectionCheck:  true,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}

	if err := client.validate(); err != nil {
		return nil, fmt.Errorf("error de configuracion del cliente postgresql: %w", err)
	}

	poolConfig, err := pgxpool.ParseConfig(client.connectionString)
	client.connectionString = ""
	if err != nil {
		return nil, fmt.Errorf("error interpretando configuracion postgresql: %w", err)
	}

	for index, configure := range client.poolConfigurers {
		if err := configure(poolConfig); err != nil {
			return nil, fmt.Errorf("error aplicando opcion postgresql %d: %w", index, err)
		}
	}

	// TLS se aplica después de las opciones avanzadas para impedir que estas lo
	// deshabiliten o restauren un fallback sin cifrado accidentalmente.
	if client.tlsConfigured {
		if err := applyTLSConfig(poolConfig, client.tlsConfig); err != nil {
			return nil, fmt.Errorf("error aplicando configuracion tls: %w", err)
		}
	}

	if err := validatePoolConfig(poolConfig); err != nil {
		return nil, fmt.Errorf("error validando pool postgresql: %w", err)
	}
	if client.requireTLS {
		if err := validateTLSRequired(poolConfig); err != nil {
			return nil, fmt.Errorf("error validando tls postgresql: %w", err)
		}
	}

	driver, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("error creando pool postgresql: %w", err)
	}
	client.driver = driver

	if !client.connectionCheck {
		return client, nil
	}

	if err := client.Ping(ctx); err != nil {
		driver.Close()
		return nil, fmt.Errorf("error verificando conexion con postgresql: %w", err)
	}

	return client, nil
}

func (c *Client) validate() error {
	if c == nil {
		return fmt.Errorf("cliente no puede ser nil")
	}
	if strings.TrimSpace(c.connectionString) == "" {
		return fmt.Errorf("cadena de conexion no puede estar vacía")
	}
	if c.tlsConfigured && c.tlsConfig == nil {
		return fmt.Errorf("configuracion tls no puede ser nil")
	}
	for index, configure := range c.poolConfigurers {
		if configure == nil {
			return fmt.Errorf("configurador del pool en posicion %d no puede ser nil", index)
		}
	}
	return nil
}

// Driver devuelve el pool nativo de pgx. El valor no debe cerrarse
// directamente; el propietario del wrapper debe llamar a Close.
func (c *Client) Driver() *pgxpool.Pool {
	if c == nil {
		return nil
	}
	return c.driver
}

// Ping verifica que PostgreSQL sea alcanzable.
func (c *Client) Ping(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if c == nil || c.driver == nil {
		return ErrClientUnavailable
	}
	if c.closed.Load() {
		return ErrClientClosed
	}
	if err := c.driver.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgresql: %w", err)
	}
	return nil
}

// Close cierra el pool. Es idempotente y puede invocarse desde distintas
// goroutines. pgxpool espera a que las conexiones adquiridas sean liberadas.
func (c *Client) Close() {
	if c == nil {
		return
	}

	c.closeMutex.Lock()
	defer c.closeMutex.Unlock()

	if c.closed.Load() {
		return
	}
	c.closed.Store(true)

	if c.driver != nil {
		c.driver.Close()
	}
}
