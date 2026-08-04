package logger

import (
	"fmt"
	"math"
)

const (
	defaultMaxSizeMB  = 100
	defaultMaxBackups = 5
	defaultMaxAgeDays = 14
	defaultCompress   = true
	defaultLocalTime  = false
)

// Rotation configura la rotación diaria, por tamaño y la retención de archivos.
type Rotation struct {
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
	LocalTime  bool
}

// NewRotation construye una configuración Rotation a partir de los valores
// predeterminados y aplica después las opciones funcionales proporcionadas.
func NewRotation(options ...RotationOption) (*Rotation, error) {
	rotation := &Rotation{
		MaxSizeMB:  defaultMaxSizeMB,
		MaxBackups: defaultMaxBackups,
		MaxAgeDays: defaultMaxAgeDays,
		Compress:   defaultCompress,
		LocalTime:  defaultLocalTime,
	}

	for _, opt := range options {
		if opt != nil {
			opt(rotation)
		}
	}

	if err := rotation.validate(); err != nil {
		return nil, fmt.Errorf("validate rotation configuration: %w", err)
	}

	return rotation, nil
}

func (r *Rotation) validate() error {
	if r == nil {
		return fmt.Errorf("rotation configuration cannot be nil")
	}
	if r.MaxSizeMB <= 0 {
		return fmt.Errorf("rotation max size must be greater than zero")
	}
	if int64(r.MaxSizeMB) > math.MaxInt64/bytesPerMB {
		return fmt.Errorf("rotation max size is too large")
	}
	if r.MaxBackups < 0 {
		return fmt.Errorf("rotation max backups cannot be negative")
	}
	if r.MaxAgeDays < 0 {
		return fmt.Errorf("rotation max age cannot be negative")
	}
	return nil
}
