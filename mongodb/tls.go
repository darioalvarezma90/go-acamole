package mongodb

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"github.com/youmark/pkcs8"
)

// TLS contiene una configuración TLS ya cargada y validada. Los paths y la
// contraseña solamente se utilizan durante NewTLS.
type TLS struct {
	caFile         string
	clientCertFile string
	clientKeyFile  string
	serverName     string

	clientKeyPassword    []byte
	clientKeyPasswordSet bool

	driverConfig *tls.Config
}

// NewTLS construye una configuración TLS mutua a partir de una CA, un
// certificado cliente y una llave cliente en formato PEM. La llave puede estar
// sin cifrar o cifrada como PKCS#8 (ENCRYPTED PRIVATE KEY).
func NewTLS(
	caFile string,
	clientCertFile string,
	clientKeyFile string,
	opts ...TLSOption,
) (*TLS, error) {
	configuration := &TLS{
		caFile:         caFile,
		clientCertFile: clientCertFile,
		clientKeyFile:  clientKeyFile,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(configuration)
		}
	}

	if err := configuration.validate(); err != nil {
		configuration.clearPassword()
		return nil, fmt.Errorf("error de configuracion tls: %w", err)
	}

	driverConfig, err := configuration.build()
	configuration.clearPassword()
	if err != nil {
		return nil, fmt.Errorf("error construyendo configuracion tls: %w", err)
	}

	configuration.driverConfig = driverConfig
	return configuration, nil
}

// validate comprueba que las rutas y credenciales TLS requeridas estén presentes.
func (configuration *TLS) validate() error {
	if configuration == nil {
		return fmt.Errorf("configuracion tls no puede ser nil")
	}
	if strings.TrimSpace(configuration.caFile) == "" {
		return fmt.Errorf("archivo de autoridad certificadora no puede estar vacío")
	}
	if strings.TrimSpace(configuration.clientCertFile) == "" {
		return fmt.Errorf("archivo de certificado cliente no puede estar vacío")
	}
	if strings.TrimSpace(configuration.clientKeyFile) == "" {
		return fmt.Errorf("archivo de llave cliente no puede estar vacío")
	}
	if configuration.serverName != "" && strings.TrimSpace(configuration.serverName) != configuration.serverName {
		return fmt.Errorf("nombre de servidor tls no puede contener espacios al inicio o al final")
	}
	return nil
}

// build carga los certificados y construye la configuración TLS del driver.
func (configuration *TLS) build() (*tls.Config, error) {
	caPEM, err := os.ReadFile(configuration.caFile)
	if err != nil {
		return nil, fmt.Errorf("leyendo certificado de autoridad: %w", err)
	}

	caPool := x509.NewCertPool()
	if ok := caPool.AppendCertsFromPEM(caPEM); !ok {
		return nil, fmt.Errorf("archivo de autoridad no contiene certificados PEM válidos")
	}

	clientCertificatePEM, err := os.ReadFile(configuration.clientCertFile)
	if err != nil {
		return nil, fmt.Errorf("leyendo certificado cliente: %w", err)
	}
	rawClientKeyPEM, err := os.ReadFile(configuration.clientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("leyendo llave privada cliente: %w", err)
	}
	defer clearBytes(rawClientKeyPEM)

	clientKeyPEM, err := configuration.prepareClientKey(rawClientKeyPEM)
	if err != nil {
		return nil, err
	}
	defer clearBytes(clientKeyPEM)

	clientCertificate, err := tls.X509KeyPair(clientCertificatePEM, clientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("cargando certificado y llave cliente: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      caPool,
		Certificates: []tls.Certificate{clientCertificate},
		ServerName:   configuration.serverName,
	}, nil
}

// prepareClientKey procesa todos los bloques PEM del archivo. Descifra una llave
// PKCS#8 únicamente en memoria manteniendo el resto de bloques intactos,
// devolviendo una codificación que tls.X509KeyPair pueda procesar.
func (configuration *TLS) prepareClientKey(clientKeyPEM []byte) ([]byte, error) {
	var resultPEM []byte
	remaining := clientKeyPEM
	privateKeyCount := 0

	for len(remaining) > 0 {
		var block *pem.Block
		block, remaining = pem.Decode(remaining)
		if block == nil {
			break
		}

		if !strings.Contains(block.Type, "PRIVATE KEY") {
			resultPEM = append(resultPEM, pem.EncodeToMemory(block)...)
			continue
		}

		privateKeyCount++
		if privateKeyCount > 1 {
			return nil, fmt.Errorf("archivo de llave cliente contiene más de una llave privada PEM")
		}

		switch block.Type {
		case "ENCRYPTED PRIVATE KEY":
			if !configuration.clientKeyPasswordSet {
				return nil, fmt.Errorf("la llave cliente está cifrada pero no se proporcionó contraseña")
			}

			privateKey, err := pkcs8.ParsePKCS8PrivateKey(
				block.Bytes,
				configuration.clientKeyPassword,
			)
			if err != nil {
				return nil, fmt.Errorf("descifrando llave privada cliente PKCS#8: %w", err)
			}

			privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
			if err != nil {
				return nil, fmt.Errorf("codificando llave privada cliente PKCS#8: %w", err)
			}
			defer clearBytes(privateKeyDER)

			resultPEM = append(resultPEM, pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: privateKeyDER,
			})...)

		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if isLegacyEncryptedPEM(block) {
				return nil, fmt.Errorf(
					"la llave usa cifrado PEM legado; conviértala a PKCS#8 cifrado",
				)
			}
			resultPEM = append(resultPEM, pem.EncodeToMemory(block)...)

		default:
			return nil, fmt.Errorf("tipo de llave privada no soportado: %q", block.Type)
		}
	}

	if privateKeyCount == 0 {
		return nil, fmt.Errorf("archivo de llave cliente no contiene una llave privada PEM")
	}

	return resultPEM, nil
}

// isLegacyEncryptedPEM indica si un bloque PEM usa el cifrado heredado.
func isLegacyEncryptedPEM(block *pem.Block) bool {
	if block == nil {
		return false
	}
	_, hasProcType := block.Headers["Proc-Type"]
	_, hasDEKInfo := block.Headers["DEK-Info"]
	return hasProcType || hasDEKInfo
}

// clone devuelve una copia independiente de la configuración TLS construida.
func (configuration *TLS) clone() *tls.Config {
	if configuration == nil || configuration.driverConfig == nil {
		return nil
	}
	return configuration.driverConfig.Clone()
}

// clearPassword borra de memoria la contraseña almacenada para la llave cliente.
func (configuration *TLS) clearPassword() {
	clearBytes(configuration.clientKeyPassword)
	configuration.clientKeyPassword = nil
	configuration.clientKeyPasswordSet = false
}

// clearBytes sobrescribe con ceros el contenido de un bloque de memoria.
func clearBytes(data []byte) {
	for index := range data {
		data[index] = 0
	}
}
