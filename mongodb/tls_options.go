package mongodb

// TLSOption configura TLS durante su construcción.
type TLSOption func(*TLS)

// WithClientKeyPassword establece la contraseña de la llave privada. Se usa
// []byte para poder sobrescribir la copia interna después de construir TLS.
func WithClientKeyPassword(password []byte) TLSOption {
	return func(configuration *TLS) {
		configuration.clearPassword()
		configuration.clientKeyPassword = append([]byte(nil), password...)
		configuration.clientKeyPasswordSet = true
	}
}

// WithTLSServerName establece explícitamente el nombre utilizado para validar
// el certificado del servidor. Normalmente el driver lo infiere del hostname.
func WithTLSServerName(serverName string) TLSOption {
	return func(configuration *TLS) {
		configuration.serverName = serverName
	}
}
