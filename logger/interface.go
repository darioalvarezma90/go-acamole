package logger

// ILogger define los métodos estándar de registro.
// Mantiene el formato (mensaje, clave, valor, clave, valor...)
type ILogger interface {
	Debug(message string, args ...any)
	Info(message string, args ...any)
	Warn(message string, args ...any)
	Error(message string, args ...any)
}

// Valida en tiempo de compilación si Logger implementa ILogger.
var _ ILogger = (*Logger)(nil)
