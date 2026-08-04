package logger

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestNewRotationDefaults(t *testing.T) {
	rotation, err := NewRotation(nil)
	if err != nil {
		t.Fatalf("NewRotation() error = %v", err)
	}

	if rotation.MaxSizeMB != defaultMaxSizeMB {
		t.Errorf("MaxSizeMB = %d, want %d", rotation.MaxSizeMB, defaultMaxSizeMB)
	}
	if rotation.MaxBackups != defaultMaxBackups {
		t.Errorf("MaxBackups = %d, want %d", rotation.MaxBackups, defaultMaxBackups)
	}
	if rotation.MaxAgeDays != defaultMaxAgeDays {
		t.Errorf("MaxAgeDays = %d, want %d", rotation.MaxAgeDays, defaultMaxAgeDays)
	}
	if rotation.Compress != defaultCompress {
		t.Errorf("Compress = %v, want %v", rotation.Compress, defaultCompress)
	}
	if rotation.LocalTime != defaultLocalTime {
		t.Errorf("LocalTime = %v, want %v", rotation.LocalTime, defaultLocalTime)
	}
}

func TestNewRotationAppliesOptions(t *testing.T) {
	rotation, err := NewRotation(
		WithMaxSizeMB(12),
		WithMaxBackups(3),
		WithMaxAgeDays(7),
		WithCompression(false),
		WithLocalTime(true),
	)
	if err != nil {
		t.Fatalf("NewRotation() error = %v", err)
	}

	if rotation.MaxSizeMB != 12 || rotation.MaxBackups != 3 || rotation.MaxAgeDays != 7 {
		t.Fatalf("numeric rotation options were not applied: %+v", rotation)
	}
	if rotation.Compress {
		t.Error("WithCompression(false) was not applied")
	}
	if !rotation.LocalTime {
		t.Error("WithLocalTime(true) was not applied")
	}
}

func TestRotationValidation(t *testing.T) {
	valid := Rotation{MaxSizeMB: 1, MaxBackups: 0, MaxAgeDays: 0}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid rotation rejected: %v", err)
	}

	tests := []struct {
		name        string
		rotation    *Rotation
		wantInError string
	}{
		{name: "nil", rotation: nil, wantInError: "cannot be nil"},
		{name: "zero size", rotation: &Rotation{MaxSizeMB: 0}, wantInError: "greater than zero"},
		{name: "negative size", rotation: &Rotation{MaxSizeMB: -1}, wantInError: "greater than zero"},
		{name: "negative backups", rotation: &Rotation{MaxSizeMB: 1, MaxBackups: -1}, wantInError: "backups"},
		{name: "negative age", rotation: &Rotation{MaxSizeMB: 1, MaxAgeDays: -1}, wantInError: "age"},
	}

	if strconv.IntSize == 64 {
		tests = append(tests, struct {
			name        string
			rotation    *Rotation
			wantInError string
		}{
			name:        "size overflow",
			rotation:    &Rotation{MaxSizeMB: int(math.MaxInt64/bytesPerMB + 1)},
			wantInError: "too large",
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.rotation.validate()
			if err == nil {
				t.Fatal("validate() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("validate() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}
}

func TestNewRotationWrapsValidationError(t *testing.T) {
	_, err := NewRotation(WithMaxSizeMB(0))
	if err == nil {
		t.Fatal("NewRotation() error = nil")
	}
	if !strings.Contains(err.Error(), "validate rotation configuration") {
		t.Fatalf("NewRotation() error = %q, want validation context", err)
	}
}
