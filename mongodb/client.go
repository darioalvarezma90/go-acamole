package mongodb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const defaultCleanupTimeout = 5 * time.Second

var (
	// ErrNilContext indica que se recibió un contexto nil.
	ErrNilContext = errors.New("contexto no puede ser nil")

	// ErrClientClosed indica que se intentó usar un cliente ya cerrado.
	ErrClientClosed = errors.New("cliente mongodb cerrado")

	// ErrClientUnavailable indica que el wrapper no contiene un driver válido.
	ErrClientUnavailable = errors.New("cliente mongodb no disponible")
)

// Client administra un cliente compartido del driver oficial de MongoDB.
// Tanto mongo.Client como mongo.Database pueden compartirse entre goroutines.
type Client struct {
	uri                string
	databaseName       string
	connectionCheck    bool
	pingReadPreference *readpref.ReadPref
	driverOptions      []*options.ClientOptions

	tlsConfigured bool
	tlsConfig     *tls.Config

	driver   *mongo.Client
	database *mongo.Database

	closeOnce sync.Once
	closed    atomic.Bool
	closeErr  error
}

// NewClient construye un cliente MongoDB.
func NewClient(
	ctx context.Context,
	uri string,
	databaseName string,
	opts ...ClientOption,
) (*Client, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	client := &Client{
		uri:                uri,
		databaseName:       databaseName,
		connectionCheck:    true,
		pingReadPreference: readpref.Primary(),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}

	if err := client.validate(); err != nil {
		return nil, fmt.Errorf("error de configuracion del cliente mongodb: %w", err)
	}

	if err := client.connect(); err != nil {
		return nil, err
	}

	if !client.connectionCheck {
		return client, nil
	}

	if err := client.Ping(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultCleanupTimeout)
		defer cancel()

		if closeErr := client.Close(cleanupCtx); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}

	return client, nil
}

// connect encapsula la configuración e instanciación del driver oficial.
func (c *Client) connect() error {
	defer func() {
		clear(c.driverOptions)
		c.driverOptions = nil
	}()

	driverOptions := make([]*options.ClientOptions, 0, 2+len(c.driverOptions))
	driverOptions = append(driverOptions, options.Client().ApplyURI(c.uri))
	driverOptions = append(driverOptions, c.driverOptions...)

	if c.tlsConfig != nil {
		driverOptions = append(
			driverOptions,
			options.Client().SetTLSConfig(c.tlsConfig.Clone()),
		)
	}

	driver, err := mongo.Connect(driverOptions...)
	if err != nil {
		return fmt.Errorf("error creando cliente mongodb: %w", err)
	}

	c.driver = driver
	c.database = driver.Database(c.databaseName)
	return nil
}

// validate comprueba que la configuración interna del cliente sea válida.
func (c *Client) validate() error {
	if c == nil {
		return fmt.Errorf("cliente no puede ser nil")
	}
	if strings.TrimSpace(c.uri) == "" {
		return fmt.Errorf("uri no puede estar vacío")
	}
	if strings.TrimSpace(c.databaseName) == "" {
		return fmt.Errorf("nombre de base de datos no puede estar vacío")
	}
	if c.tlsConfigured && c.tlsConfig == nil {
		return fmt.Errorf("configuracion tls no puede ser nil")
	}
	for index, driverOption := range c.driverOptions {
		if driverOption == nil {
			return fmt.Errorf("opcion del driver en posicion %d no puede ser nil", index)
		}
		if driverOption.GetURI() != "" {
			return fmt.Errorf(
				"opcion del driver en posicion %d no puede definir una uri; use el argumento uri de NewClient",
				index,
			)
		}
	}
	return nil
}

// Driver devuelve el cliente nativo de MongoDB de forma segura concurrente.
func (c *Client) Driver() *mongo.Client {
	if c == nil {
		return nil
	}
	return c.driver
}

// Database devuelve la base de datos de forma segura concurrente.
func (c *Client) Database() *mongo.Database {
	if c == nil {
		return nil
	}
	return c.database
}

// Ping verifica que el deployment configurado sea alcanzable.
func (c *Client) Ping(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if c == nil {
		return ErrClientUnavailable
	}

	driver := c.driver

	if driver == nil {
		return ErrClientUnavailable
	}
	if c.closed.Load() {
		return ErrClientClosed
	}

	if err := driver.Ping(ctx, c.pingReadPreference); err != nil {
		if c.closed.Load() {
			return ErrClientClosed
		}
		return fmt.Errorf("ping mongodb: %w", err)
	}

	if c.closed.Load() {
		return ErrClientClosed
	}

	return nil
}

// Close desconecta el cliente de forma idempotente.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return ErrNilContext
	}

	c.closeOnce.Do(func() {
		c.closed.Store(true)

		driver := c.driver

		if driver != nil {
			if err := driver.Disconnect(ctx); err != nil {
				c.closeErr = fmt.Errorf("error cerrando cliente mongodb: %w", err)
			}
		}
	})

	return c.closeErr
}
