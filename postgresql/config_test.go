package postgresql

import (
	"crypto/tls"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApplyTLSConfigSecuresMultipleHosts(t *testing.T) {
	configuration, err := pgxpool.ParseConfig(
		"host=db-one.example.com,db-two.example.com " +
			"port=5432,5433 user=test dbname=test sslmode=prefer",
	)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if err := applyTLSConfig(configuration, &tls.Config{}); err != nil {
		t.Fatalf("applyTLSConfig() error = %v", err)
	}

	if configuration.ConnConfig.TLSConfig == nil {
		t.Fatal("primary TLSConfig = nil")
	}
	if configuration.ConnConfig.TLSConfig.ServerName != "db-one.example.com" {
		t.Errorf("primary ServerName = %q, want db-one.example.com", configuration.ConnConfig.TLSConfig.ServerName)
	}
	if len(configuration.ConnConfig.Fallbacks) != 1 {
		t.Fatalf("fallback count = %d, want 1", len(configuration.ConnConfig.Fallbacks))
	}
	fallback := configuration.ConnConfig.Fallbacks[0]
	if fallback.Host != "db-two.example.com" || fallback.Port != 5433 {
		t.Errorf("fallback = %s:%d, want db-two.example.com:5433", fallback.Host, fallback.Port)
	}
	if fallback.TLSConfig == nil || fallback.TLSConfig.ServerName != "db-two.example.com" {
		t.Errorf("fallback TLS ServerName = %v, want db-two.example.com", fallback.TLSConfig)
	}
}

func TestApplyTLSConfigRejectsOldTLSVersion(t *testing.T) {
	configuration, err := pgxpool.ParseConfig(offlineConnectionString)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	err = applyTLSConfig(configuration, &tls.Config{MinVersion: tls.VersionTLS11})
	if err == nil {
		t.Fatal("applyTLSConfig() error = nil")
	}
	if !strings.Contains(err.Error(), "1.2") {
		t.Fatalf("applyTLSConfig() error = %q, want TLS 1.2 message", err)
	}
}

func TestApplyTLSConfigRejectsIncompatibleTLSVersions(t *testing.T) {
	configuration, err := pgxpool.ParseConfig(offlineConnectionString)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	err = applyTLSConfig(configuration, &tls.Config{
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS12,
	})
	if err == nil {
		t.Fatal("applyTLSConfig() error = nil")
	}
	if !strings.Contains(err.Error(), "no puede superar") {
		t.Fatalf("applyTLSConfig() error = %q, want incompatible-version message", err)
	}
}

func TestApplyTLSConfigRejectsPaddedServerName(t *testing.T) {
	configuration, err := pgxpool.ParseConfig(offlineConnectionString)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	err = applyTLSConfig(configuration, &tls.Config{ServerName: " localhost"})
	if err == nil {
		t.Fatal("applyTLSConfig() error = nil")
	}
	if !strings.Contains(err.Error(), "ServerName") {
		t.Fatalf("applyTLSConfig() error = %q, want ServerName message", err)
	}
}

func TestApplyTLSConfigRequiresServerNameForUnixSocket(t *testing.T) {
	configuration, err := pgxpool.ParseConfig(
		"host=/var/run/postgresql user=test dbname=test sslmode=disable",
	)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	err = applyTLSConfig(configuration, &tls.Config{})
	if err == nil {
		t.Fatal("applyTLSConfig() error = nil")
	}
	if !strings.Contains(err.Error(), "ServerName") {
		t.Fatalf("applyTLSConfig() error = %q, want ServerName message", err)
	}
}

func TestRequireTLSAcceptsVerifyFull(t *testing.T) {
	configuration, err := pgxpool.ParseConfig(
		"postgres://test@db.example.com:5432/testdb?sslmode=verify-full",
	)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if err := validateTLSRequired(configuration); err != nil {
		t.Fatalf("validateTLSRequired() error = %v", err)
	}
}
