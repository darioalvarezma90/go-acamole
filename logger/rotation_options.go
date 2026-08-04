package logger

// RotationOption configura una Rotation durante su construcción.
type RotationOption func(*Rotation)

// WithMaxSizeMB establece el tamaño máximo, en megabytes, del archivo activo.
func WithMaxSizeMB(size int) RotationOption {
	return func(rotation *Rotation) {
		rotation.MaxSizeMB = size
	}
}

// WithMaxBackups establece la cantidad máxima de archivos rotados que se conservarán.
func WithMaxBackups(files int) RotationOption {
	return func(rotation *Rotation) {
		rotation.MaxBackups = files
	}
}

// WithMaxAgeDays establece la antigüedad máxima, en días, de un archivo rotado.
func WithMaxAgeDays(days int) RotationOption {
	return func(rotation *Rotation) {
		rotation.MaxAgeDays = days
	}
}

// WithCompression habilita o deshabilita la compresión gzip de los archivos rotados.
func WithCompression(enabled bool) RotationOption {
	return func(rotation *Rotation) {
		rotation.Compress = enabled
	}
}

// WithLocalTime habilita o deshabilita marcas de tiempo locales en los nombres.
func WithLocalTime(enabled bool) RotationOption {
	return func(rotation *Rotation) {
		rotation.LocalTime = enabled
	}
}
