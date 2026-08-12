package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultAppName      = "go-app"
	defaultOutput       = ConsoleOutput
	defaultLevel        = DebugLevel
	defaultEncoding     = JSONEncoding
	defaultLogDirectory = "./logs"
)

// Logger es la estructura principal compartida entre goroutines.
type Logger struct {
	appName       string
	directory     string
	fileName      string
	output        Output
	level         Level
	encoding      Encoding
	driver        *zap.SugaredLogger
	rotation      *Rotation
	consoleWriter io.Writer
	fileWriter    *dailyWriter
	closeMutex    sync.Mutex
	closed        atomic.Bool
	closeErr      error
}

// NewLogger construye un registrador con las opciones funcionales proporcionadas.
func NewLogger(appName string, options ...LoggerOption) (*Logger, error) {
	if appName == "" {
		appName = defaultAppName
	}

	defaultRot, err := NewRotation()
	if err != nil {
		return nil, fmt.Errorf("error creando configuracion de rotacion: %w", err)
	}

	l := &Logger{
		appName:       appName,
		fileName:      appName,
		output:        defaultOutput,
		level:         defaultLevel,
		directory:     defaultLogDirectory,
		encoding:      defaultEncoding,
		rotation:      defaultRot,
		consoleWriter: os.Stdout,
	}

	for _, opt := range options {
		if opt != nil {
			opt(l)
		}
	}

	if err := l.validate(); err != nil {
		return nil, fmt.Errorf("error de configuracion: %w", err)
	}

	// 1. Configurar el Nivel de Log
	var zapLevel zapcore.Level
	switch l.level {
	case DebugLevel:
		zapLevel = zapcore.DebugLevel
	case InfoLevel:
		zapLevel = zapcore.InfoLevel
	case WarnLevel:
		zapLevel = zapcore.WarnLevel
	case ErrorLevel:
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	// 2. Configurar el formato del Encoder (JSON o Texto)
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder // Formato de hora legible

	var encoder zapcore.Encoder
	if l.encoding == JSONEncoding {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		// Formato de texto plano legible
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// 3. Configurar los destinos (Cores)
	var cores []zapcore.Core

	if l.output.includes(ConsoleOutput) {
		consoleCore := zapcore.NewCore(
			encoder,
			zapcore.Lock(zapcore.AddSync(l.consoleWriter)),
			zapLevel,
		)
		cores = append(cores, consoleCore)
	}

	if l.output.includes(FileOutput) {
		if err := os.MkdirAll(l.directory, 0755); err != nil {
			return nil, fmt.Errorf("error creando directorio de logs: %w", err)
		}

		l.fileWriter, err = newDailyWriter(l.directory, l.fileName, l.rotation)
		if err != nil {
			return nil, fmt.Errorf("error creando writer de archivo: %w", err)
		}
		fileCore := zapcore.NewCore(
			encoder,
			zapcore.AddSync(l.fileWriter),
			zapLevel,
		)
		cores = append(cores, fileCore)
	}

	// 4. Construir el Logger final uniendo las salidas
	core := zapcore.NewTee(cores...)

	// Agregamos el nombre de la app como campo base (equivalente a With("app", appName))
	baseLogger := zap.New(core).With(zap.String("app", l.appName))

	l.driver = baseLogger.Sugar() // Usamos Sugar para la API amigable (args ...any)

	return l, nil
}

// validate comprueba que la configuración interna del registrador sea válida.
func (l *Logger) validate() error {
	if l == nil {
		return fmt.Errorf("logger no puede ser nil")
	}
	if strings.TrimSpace(l.appName) == "" {
		return fmt.Errorf("nombre app inválido")
	}
	if strings.TrimSpace(l.appName) != l.appName {
		return fmt.Errorf("nombre app no puede contener espacios al inicio o al final")
	}
	if !l.output.isValid() {
		return fmt.Errorf("tipo de output invalido")
	}
	if !l.level.isValid() {
		return fmt.Errorf("nivel de log invalido")
	}
	if !l.encoding.isValid() {
		return fmt.Errorf("codificacion invalida")
	}
	if l.output.includes(ConsoleOutput) && l.consoleWriter == nil {
		return fmt.Errorf("writer de consola no puede ser nil")
	}
	if l.output.includes(FileOutput) {
		if strings.TrimSpace(l.directory) == "" {
			return fmt.Errorf("directorio de logs inválido")
		}
		if filepath.Base(l.fileName) != l.fileName {
			return fmt.Errorf("nombre de archivo no puede incluir directorios")
		}
		normalizedFileName := normalizeBaseName(l.fileName)
		if strings.TrimSpace(normalizedFileName) == "" || normalizedFileName == "." || normalizedFileName == ".." {
			return fmt.Errorf("nombre de archivo inválido")
		}
		l.fileName = normalizedFileName
		if l.rotation == nil {
			return fmt.Errorf("configuración de rotación no puede ser nil")
		}
		if err := l.rotation.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Close impide nuevas escrituras, sincroniza el archivo activo y libera recursos.
// Es seguro llamarlo varias veces y desde goroutines distintas.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}

	l.closeMutex.Lock()
	defer l.closeMutex.Unlock()

	if l.closed.Load() {
		return l.closeErr
	}
	l.closed.Store(true)

	if l.fileWriter != nil {
		if err := l.fileWriter.Close(); err != nil {
			l.closeErr = fmt.Errorf("error cerrando archivo de logs: %w", err)
		}
	}
	return l.closeErr
}

// Sync fuerza la persistencia del archivo activo. Los cores de Zap utilizados
// por este paquete no agregan buffering propio.
func (l *Logger) Sync() error {
	if l == nil || l.fileWriter == nil {
		return nil
	}
	return l.fileWriter.Sync()
}

// MaintenanceError devuelve el error más reciente producido al aplicar la
// retención de archivos antiguos. Estos fallos no interrumpen las escrituras.
func (l *Logger) MaintenanceError() error {
	if l == nil || l.fileWriter == nil {
		return nil
	}
	return l.fileWriter.MaintenanceError()
}

// canLog indica si el registrador está listo para aceptar nuevas entradas.
func (l *Logger) canLog() bool {
	return l != nil && l.driver != nil && !l.closed.Load()
}

// Debug registra un mensaje con nivel de depuración y sus campos estructurados.
func (l *Logger) Debug(message string, args ...any) {
	if l.canLog() {
		l.driver.Debugw(message, args...)
	}
}

// Info registra un mensaje informativo y sus campos estructurados.
func (l *Logger) Info(message string, args ...any) {
	if l.canLog() {
		l.driver.Infow(message, args...)
	}
}

// Warn registra un mensaje de advertencia y sus campos estructurados.
func (l *Logger) Warn(message string, args ...any) {
	if l.canLog() {
		l.driver.Warnw(message, args...)
	}
}

// Error registra un mensaje de error y sus campos estructurados.
func (l *Logger) Error(message string, args ...any) {
	if l.canLog() {
		l.driver.Errorw(message, args...)
	}
}

// Funciones extra (utilidades)
func ParseLogLevel(value string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return DebugLevel, nil
	case "info":
		return InfoLevel, nil
	case "warn", "warning":
		return WarnLevel, nil
	case "error":
		return ErrorLevel, nil
	default:
		return 0, fmt.Errorf("nivel de log inválido: %q", value)
	}
}

func ParseLogOutput(value string) (Output, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "console":
		return ConsoleOutput, nil
	case "file":
		return FileOutput, nil
	case "file+console", "console+file":
		return ConsoleAndFileOutput, nil
	default:
		return 0, fmt.Errorf("output de log inválido: %q", value)
	}
}
