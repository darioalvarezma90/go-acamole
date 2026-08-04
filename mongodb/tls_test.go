package mongodb

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/youmark/pkcs8"
)

func TestNewTLSLoadsUnencryptedKey(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, nil)

	configuration, err := NewTLS(
		caFile,
		certFile,
		keyFile,
		WithTLSServerName("mongo.example.com"),
	)
	if err != nil {
		t.Fatalf("NewTLS() error = %v", err)
	}

	if configuration.driverConfig == nil {
		t.Fatal("NewTLS() driverConfig = nil")
	}
	if configuration.driverConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("TLS minimum version = %d, want TLS 1.2", configuration.driverConfig.MinVersion)
	}
	if configuration.driverConfig.ServerName != "mongo.example.com" {
		t.Errorf("TLS server name = %q, want mongo.example.com", configuration.driverConfig.ServerName)
	}
	if configuration.driverConfig.RootCAs == nil {
		t.Error("TLS root CAs = nil")
	}
	if len(configuration.driverConfig.Certificates) != 1 {
		t.Errorf("TLS certificates = %d, want 1", len(configuration.driverConfig.Certificates))
	}
}

func TestNewTLSDecryptsPKCS8Key(t *testing.T) {
	password := []byte("correct-horse-battery-staple")
	caFile, certFile, keyFile := writeTLSMaterial(t, password)

	configuration, err := NewTLS(
		caFile,
		certFile,
		keyFile,
		WithClientKeyPassword(password),
	)
	if err != nil {
		t.Fatalf("NewTLS() error = %v", err)
	}
	if configuration.driverConfig == nil {
		t.Fatal("NewTLS() driverConfig = nil")
	}
	if configuration.clientKeyPassword != nil || configuration.clientKeyPasswordSet {
		t.Error("NewTLS() retained the client key password")
	}
	if string(password) != "correct-horse-battery-staple" {
		t.Error("NewTLS() modified the caller's password")
	}
}

func TestNewTLSRejectsEncryptedKeyWithoutPassword(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, []byte("secret"))

	_, err := NewTLS(caFile, certFile, keyFile)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "no se proporcionó contraseña") {
		t.Fatalf("NewTLS() error = %q, want missing-password message", err)
	}
}

func TestNewTLSRejectsIncorrectPassword(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, []byte("correct"))

	_, err := NewTLS(
		caFile,
		certFile,
		keyFile,
		WithClientKeyPassword([]byte("incorrect")),
	)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "descifrando llave privada cliente PKCS#8") {
		t.Fatalf("NewTLS() error = %q, want decrypting-key message", err)
	}
}

func TestNewTLSRejectsInvalidCA(t *testing.T) {
	_, certFile, keyFile := writeTLSMaterial(t, nil)
	invalidCAFile := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(invalidCAFile, []byte("not a certificate"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := NewTLS(invalidCAFile, certFile, keyFile)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "no contiene certificados PEM válidos") {
		t.Fatalf("NewTLS() error = %q, want invalid-CA message", err)
	}
}

func TestNewTLSRejectsLegacyEncryptedPEM(t *testing.T) {
	caFile, certFile, _ := writeTLSMaterial(t, nil)
	legacyKeyFile := filepath.Join(t.TempDir(), "legacy-key.pem")
	legacyPEM := pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Bytes:   []byte("encrypted key material"),
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,00"},
	})
	if err := os.WriteFile(legacyKeyFile, legacyPEM, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := NewTLS(
		caFile,
		certFile,
		legacyKeyFile,
		WithClientKeyPassword([]byte("secret")),
	)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "cifrado PEM legado") {
		t.Fatalf("NewTLS() error = %q, want legacy-PEM message", err)
	}
}

func TestWithTLSCopiesConfiguration(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, nil)
	configuration, err := NewTLS(caFile, certFile, keyFile)
	if err != nil {
		t.Fatalf("NewTLS() error = %v", err)
	}

	client := &Client{}
	WithTLS(configuration)(client)

	if !client.tlsConfigured {
		t.Error("WithTLS() did not mark TLS as configured")
	}
	if client.tlsConfig == nil {
		t.Fatal("WithTLS() tlsConfig = nil")
	}
	if client.tlsConfig == configuration.driverConfig {
		t.Error("WithTLS() retained the caller's tls.Config pointer")
	}
}

func TestNewTLSPreservesAdditionalPEMBlocks(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, nil)
	certificatePEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("ReadFile(certificate) error = %v", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("ReadFile(key) error = %v", err)
	}

	combinedKeyFile := filepath.Join(t.TempDir(), "combined-key.pem")
	combinedPEM := append(append([]byte(nil), certificatePEM...), keyPEM...)
	if err := os.WriteFile(combinedKeyFile, combinedPEM, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := NewTLS(caFile, certFile, combinedKeyFile); err != nil {
		t.Fatalf("NewTLS() error = %v", err)
	}
}

func TestNewTLSRejectsMultiplePrivateKeys(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, nil)
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("ReadFile(key) error = %v", err)
	}

	multipleKeysFile := filepath.Join(t.TempDir(), "multiple-keys.pem")
	multipleKeysPEM := append(append([]byte(nil), keyPEM...), keyPEM...)
	if err := os.WriteFile(multipleKeysFile, multipleKeysPEM, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = NewTLS(caFile, certFile, multipleKeysFile)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "más de una llave privada") {
		t.Fatalf("NewTLS() error = %q, want multiple-keys message", err)
	}
}

func TestNewTLSRejectsUnsupportedPrivateKeyType(t *testing.T) {
	caFile, certFile, _ := writeTLSMaterial(t, nil)
	unsupportedKeyFile := filepath.Join(t.TempDir(), "unsupported-key.pem")
	unsupportedKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: []byte("unsupported key material"),
	})
	if err := os.WriteFile(unsupportedKeyFile, unsupportedKeyPEM, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := NewTLS(caFile, certFile, unsupportedKeyFile)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "tipo de llave privada no soportado") {
		t.Fatalf("NewTLS() error = %q, want unsupported-key message", err)
	}
}

func TestNewTLSRejectsBlankServerName(t *testing.T) {
	caFile, certFile, keyFile := writeTLSMaterial(t, nil)

	_, err := NewTLS(
		caFile,
		certFile,
		keyFile,
		WithTLSServerName("   "),
	)
	if err == nil {
		t.Fatal("NewTLS() error = nil")
	}
	if !strings.Contains(err.Error(), "servidor tls") {
		t.Fatalf("NewTLS() error = %q, want invalid-server-name message", err)
	}
}

func writeTLSMaterial(t *testing.T, password []byte) (string, string, string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(CA) error = %v", err)
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(client) error = %v", err)
	}

	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(CA) error = %v", err)
	}

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(
		rand.Reader,
		clientTemplate,
		caTemplate,
		&clientKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("CreateCertificate(client) error = %v", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	clientPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})

	var keyDER []byte
	keyType := "PRIVATE KEY"
	if password == nil {
		keyDER, err = x509.MarshalPKCS8PrivateKey(clientKey)
	} else {
		keyDER, err = pkcs8.MarshalPrivateKey(clientKey, password, nil)
		keyType = "ENCRYPTED PRIVATE KEY"
	}
	if err != nil {
		t.Fatalf("marshal client key error = %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: keyType, Bytes: keyDER})

	directory := t.TempDir()
	caFile := filepath.Join(directory, "ca.pem")
	certFile := filepath.Join(directory, "client-cert.pem")
	keyFile := filepath.Join(directory, "client-key.pem")

	for path, data := range map[string][]byte{
		caFile:   caPEM,
		certFile: clientPEM,
		keyFile:  keyPEM,
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	return caFile, certFile, keyFile
}
