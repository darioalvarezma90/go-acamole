package postgresql

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Aísla la configuración local para que estas pruebas no dependan del equipo.
func isolateConnectionEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSERVICE",
		"PGSERVICEFILE", "PGSSLMODE", "PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT",
		"PGSSLPASSWORD", "PGOPTIONS", "PGAPPNAME", "PGCONNECT_TIMEOUT",
		"PGTARGETSESSIONATTRS", "PGTZ", "PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "missing-pgpass"))
	t.Setenv("PGSSLMODE", "disable")
}

func TestConnectionOptionsOverrideBaseAndPreserveCredentials(t *testing.T) {
	isolateConnectionEnvironment(t)
	t.Setenv("PGHOST", "environment.example")
	t.Setenv("PGUSER", "environment-user")
	t.Setenv("PGPASSWORD", "environment-password")
	const password = " p@ss:/?#&=+'\\ word\n "
	for _, base := range []string{
		"",
		"postgres://old:old@old.example:5432/old?database=alias&user=query&password=query&application_name=preserved",
		"postgresql://old:old@old.example:5432/old?dbname=alias",
		"host=old.example port=5432 user=old password=old dbname=old",
	} {
		t.Run(base, func(t *testing.T) {
			client, err := NewClientWithOptions(context.Background(),
				WithHost("db.example"), WithPort(6432), WithUser("app user"),
				WithPassword(password), WithDatabase("orders ' archive\\2026"),
				// La cadena se registra al final para comprobar la prioridad por campo.
				WithConnectionString(base), WithConnectionCheck(false),
				WithMaxConnections(17), WithMinConnections(0), WithMinIdleConnections(0),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			pool := client.Driver().Config()
			config := pool.ConnConfig
			if config.Host != "db.example" || config.Port != 6432 || config.User != "app user" ||
				config.Password != password || config.Database != "orders ' archive\\2026" {
				t.Fatal("connection options were not preserved")
			}
			if pool.MaxConns != 17 || pool.MinConns != 0 || pool.MinIdleConns != 0 {
				t.Fatal("pool options were not applied")
			}
			if strings.Contains(base, "application_name=") && config.RuntimeParams["application_name"] != "preserved" {
				t.Fatal("unrelated URI parameter was lost")
			}
			if client.connectionString != "" || client.connectionParams != nil {
				t.Fatal("wrapper retained connection parameters")
			}
		})
	}
}

func TestClientWithoutURIUsesEnvironment(t *testing.T) {
	isolateConnectionEnvironment(t)
	t.Setenv("PGHOST", "environment.example")
	t.Setenv("PGPORT", "6543")
	t.Setenv("PGUSER", "environment-user")
	t.Setenv("PGPASSWORD", "environment-password")
	t.Setenv("PGDATABASE", "environment-db")
	for _, constructor := range []func(context.Context, ...ClientOption) (*Client, error){
		NewClientWithOptions,
		func(ctx context.Context, opts ...ClientOption) (*Client, error) { return NewClient(ctx, "", opts...) },
	} {
		client, err := constructor(context.Background(), WithConnectionCheck(false))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		config := client.Driver().Config().ConnConfig
		if config.Host != "environment.example" || config.Port != 6543 || config.User != "environment-user" ||
			config.Password != "environment-password" || config.Database != "environment-db" {
			t.Fatal("PG environment settings were not applied")
		}
	}
}

func TestClientWithoutURIUsesDriverDefaults(t *testing.T) {
	isolateConnectionEnvironment(t)
	expected, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClientWithOptions(context.Background(), WithConnectionCheck(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	actual := client.Driver().Config()
	if actual.ConnConfig.Host != expected.ConnConfig.Host || actual.ConnConfig.Port != expected.ConnConfig.Port ||
		actual.ConnConfig.User != expected.ConnConfig.User || actual.MaxConns != expected.MaxConns || actual.MinConns != expected.MinConns {
		t.Fatal("pgx defaults were not preserved")
	}
}

func TestConnectionOptionsRejectInvalidValues(t *testing.T) {
	isolateConnectionEnvironment(t)
	for _, test := range []struct {
		name   string
		option ClientOption
	}{
		{"empty host", WithHost("")}, {"padded host", WithHost(" db.example")},
		{"zero port", WithPort(0)}, {"negative port", WithPort(-1)}, {"large port", WithPort(65536)},
		{"empty user", WithUser("")}, {"empty database", WithDatabase(" ")},
		{"invalid SSL mode", WithSSLMode("invalid")}, {"empty SSL mode", WithSSLMode("")},
		{"invalid URI", WithConnectionString("postgres://%")},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClientWithOptions(context.Background(), WithConnectionCheck(false), WithHost("localhost"), test.option)
			if client != nil {
				client.Close()
			}
			if err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestConnectionOptionsRebuildTLSAndFallbacks(t *testing.T) {
	isolateConnectionEnvironment(t)
	for _, mode := range []string{"prefer", "verify-full"} {
		t.Run(mode, func(t *testing.T) {
			client, err := NewClient(context.Background(),
				"postgres://old:secret@old-one.example:5432,old-two.example:5433/db",
				WithHost("new-one.example,new-two.example"), WithPort(6432),
				WithSSLMode(mode), WithConnectionCheck(false),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			config := client.Driver().Config().ConnConfig
			if config.Host != "new-one.example" || config.Port != 6432 || config.TLSConfig == nil || config.TLSConfig.ServerName != config.Host {
				t.Fatal("primary endpoint or TLS hostname is stale")
			}
			foundSecond := false
			for _, fallback := range config.Fallbacks {
				if fallback.Port != 6432 || (fallback.Host != "new-one.example" && fallback.Host != "new-two.example") {
					t.Fatal("stale fallback endpoint")
				}
				if fallback.TLSConfig != nil && fallback.TLSConfig.ServerName != fallback.Host {
					t.Fatal("stale fallback TLS hostname")
				}
				if mode == "verify-full" && (fallback.TLSConfig == nil || fallback.TLSConfig.InsecureSkipVerify) {
					t.Fatal("fallback lost TLS verification")
				}
				foundSecond = foundSecond || fallback.Host == "new-two.example"
			}
			if !foundSecond {
				t.Fatal("second host fallback missing")
			}
		})
	}
}

func TestConnectionOptionsResolvePasswordForFinalEndpoint(t *testing.T) {
	isolateConnectionEnvironment(t)
	passfile := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(passfile, []byte("new.example:6432:orders:app:matched-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGPASSFILE", passfile)
	client, err := NewClient(context.Background(), "postgres://old@old.example/old",
		WithHost("new.example"), WithPort(6432), WithUser("app"), WithDatabase("orders"), WithConnectionCheck(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if client.Driver().Config().ConnConfig.Password != "matched-secret" {
		t.Fatal("pgpass was not resolved for the final endpoint")
	}
}

func TestConnectionOptionOrderingAndEmptyPassword(t *testing.T) {
	isolateConnectionEnvironment(t)
	t.Setenv("PGPASSWORD", "environment-secret")
	client, err := NewClientWithOptions(context.Background(), nil,
		WithConnectionString("postgres://old:secret@old.example/old"),
		WithConnectionString(offlineConnectionString),
		WithHost("first.example"), WithHost("last.example"), WithPort(65535),
		WithPassword(""), WithConnectionCheck(false),
		WithPoolConfigurer(func(config *pgxpool.Config) error {
			if config.ConnConfig.Host != "last.example" || config.ConnConfig.Password != "" {
				t.Error("advanced configurer did not receive final connection settings")
			}
			config.ConnConfig.User = "advanced-user"
			return nil
		}),
		WithTLSConfig(&tls.Config{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	config := client.Driver().Config().ConnConfig
	if config.User != "advanced-user" || config.Database != "testdb" || config.Port != 65535 || config.Password != "" {
		t.Fatal("option precedence was not preserved")
	}
	if config.TLSConfig == nil || config.TLSConfig.ServerName != "last.example" {
		t.Fatal("explicit TLS configuration was not applied last")
	}
}

func TestClientWithOptionsChecksConnectionByDefault(t *testing.T) {
	isolateConnectionEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, err := NewClientWithOptions(ctx)
	if client != nil {
		client.Close()
		t.Fatal("expected no client when the initial ping fails")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClientWithOptionsPreservesPoolAndTLSValidation(t *testing.T) {
	isolateConnectionEnvironment(t)
	for _, test := range []struct {
		name    string
		options []ClientOption
	}{
		{"minimum above maximum", []ClientOption{WithMaxConnections(2), WithMinConnections(3)}},
		{"idle minimum above maximum", []ClientOption{WithMaxConnections(2), WithMinIdleConnections(3)}},
		{"required TLS disabled", []ClientOption{WithRequireTLS(), WithSSLMode("disable")}},
		{"required TLS with plaintext fallback", []ClientOption{WithRequireTLS(), WithSSLMode("prefer")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := append([]ClientOption{WithConnectionCheck(false), WithHost("localhost")}, test.options...)
			client, err := NewClientWithOptions(context.Background(), options...)
			if client != nil {
				client.Close()
			}
			if err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}
