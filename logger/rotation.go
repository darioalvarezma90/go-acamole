package logger

import (
	"fmt"
	"math"
)

const (
	defaultMaxSizeMB  = 100
	defaultMaxBackups = 5
	defaultMaxAgeDays = 14
	defaultCompress   = false
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
		return nil, fmt.Errorf("error validando configuración de rotación: %w", err)
	}

	return rotation, nil
}

// validate comprueba que los límites de rotación y retención sean válidos.
func (r *Rotation) validate() error {
	if r == nil {
		return fmt.Errorf("configuración de rotación no puede ser nil")
	}
	if r.MaxSizeMB <= 0 {
		return fmt.Errorf("tamaño máximo de rotación debe ser mayor que cero")
	}
	if int64(r.MaxSizeMB) > math.MaxInt64/bytesPerMB {
		return fmt.Errorf("tamaño máximo de rotación es demasiado grande")
	}
	if r.MaxBackups < 0 {
		return fmt.Errorf("cantidad máxima de respaldos de rotación no puede ser negativa")
	}
	if r.MaxAgeDays < 0 {
		return fmt.Errorf("antigüedad máxima de rotación no puede ser negativa")
	}
	return nil
}
