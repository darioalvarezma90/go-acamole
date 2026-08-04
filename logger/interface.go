package logger

// Interface define los métodos estándar de registro.
// Mantiene el formato (mensaje, clave, valor, clave, valor...)
type Interface interface {
	Debug(message string, args ...any)
	Info(message string, args ...any)
	Warn(message string, args ...any)
	Error(message string, args ...any)
	Fatal(message string, args ...any)
}

// Valida en tiempo de compilación si Logger implementa Interface.
var _ Interface = (*Logger)(nil)
