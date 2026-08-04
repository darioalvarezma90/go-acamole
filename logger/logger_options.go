package logger

import "io"

// LoggerOption configura un Logger durante su construcción.
type LoggerOption func(*Logger)

// WithOutput selecciona la salida a consola, archivo o ambas.
func WithOutput(output Output) LoggerOption {
	return func(logger *Logger) {
		logger.output = output
	}
}

// WithLevel establece el nivel mínimo de registros.
func WithLevel(level Level) LoggerOption {
	return func(logger *Logger) {
		logger.level = level
	}
}

// WithEncoding aplica la codificación JSON o texto.
func WithEncoding(encoding Encoding) LoggerOption {
	return func(logger *Logger) {
		logger.encoding = encoding
	}
}

// WithFileName establece el nombre base de los archivos. La extensión .log es
// opcional y la fecha diaria se agrega automáticamente.
func WithFileName(fileName string) LoggerOption {
	return func(logger *Logger) {
		logger.fileName = fileName
	}
}

// WithConsoleWriter establece el escritor utilizado por las salidas a consola.
func WithConsoleWriter(writer io.Writer) LoggerOption {
	return func(logger *Logger) {
		logger.consoleWriter = writer
	}
}

// WithDirectory establece el directorio donde se guardarán los logs.
func WithDirectory(directory string) LoggerOption {
	return func(logger *Logger) {
		logger.directory = directory
	}
}

// WithRotation establece la configuración de rotación de archivos.
func WithRotation(config *Rotation) LoggerOption {
	return func(logger *Logger) {
		if config == nil {
			logger.rotation = nil
			return
		}
		copyOfConfig := *config
		logger.rotation = &copyOfConfig
	}
}
