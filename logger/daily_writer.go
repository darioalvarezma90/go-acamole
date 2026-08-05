package logger

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dailyDateLayout = "02-01-2006"
	bytesPerMB      = 1024 * 1024
)

var activeDailyWriters = struct {
	sync.Mutex
	paths map[string]struct{}
}{
	paths: make(map[string]struct{}),
}

// dailyWriter escribe en un archivo por día y crea segmentos adicionales
// cuando el archivo activo supera el tamaño configurado.
type dailyWriter struct {
	mutex        sync.Mutex
	directory    string
	baseName     string
	maxSizeBytes int64
	maxBackups   int
	maxAgeDays   int
	compress     bool
	localTime    bool
	now          func() time.Time
	file         *os.File
	currentDate  string
	currentIndex int
	currentSize  int64
	closed       bool
	registryKey  string
}

type dailyLogFile struct {
	name       string
	path       string
	date       time.Time
	index      int
	compressed bool
}

// newDailyWriter construye un escritor con rotación diaria y por tamaño.
func newDailyWriter(directory, baseName string, rotation *Rotation) (*dailyWriter, error) {
	normalizedBaseName := normalizeBaseName(baseName)
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolviendo directorio de logs: %w", err)
	}

	registryKey := filepath.Join(filepath.Clean(absoluteDirectory), normalizedBaseName)
	activeDailyWriters.Lock()
	if _, exists := activeDailyWriters.paths[registryKey]; exists {
		activeDailyWriters.Unlock()
		return nil, fmt.Errorf("otro logger ya está utilizando %q", registryKey)
	}
	activeDailyWriters.paths[registryKey] = struct{}{}
	activeDailyWriters.Unlock()

	return &dailyWriter{
		directory:    directory,
		baseName:     normalizedBaseName,
		maxSizeBytes: int64(rotation.MaxSizeMB) * bytesPerMB,
		maxBackups:   rotation.MaxBackups,
		maxAgeDays:   rotation.MaxAgeDays,
		compress:     rotation.Compress,
		localTime:    rotation.LocalTime,
		now:          time.Now,
		registryKey:  registryKey,
	}, nil
}

// Write escribe datos en el segmento correspondiente a la fecha actual.
func (w *dailyWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}

	now := w.currentTime()
	date := now.Format(dailyDateLayout)
	if w.file == nil || w.currentDate != date {
		if err := w.openDate(date, now); err != nil {
			return 0, err
		}
	}

	if w.currentSize > 0 && w.currentSize+int64(len(data)) > w.maxSizeBytes {
		if err := w.openNextSegment(now); err != nil {
			return 0, err
		}
	}

	written, err := w.file.Write(data)
	w.currentSize += int64(written)
	return written, err
}

// Close sincroniza y cierra el archivo activo, y libera su registro de uso.
func (w *dailyWriter) Close() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
	defer w.releaseRegistration()

	if w.file == nil {
		return nil
	}

	syncErr := w.file.Sync()
	closeErr := w.file.Close()
	w.file = nil
	w.currentSize = 0

	switch {
	case syncErr != nil && closeErr != nil:
		return fmt.Errorf("sincronizando archivo diario de logs: %v; cerrando archivo diario de logs: %w", syncErr, closeErr)
	case syncErr != nil:
		return fmt.Errorf("sincronizando archivo diario de logs: %w", syncErr)
	case closeErr != nil:
		return fmt.Errorf("cerrando archivo diario de logs: %w", closeErr)
	default:
		return nil
	}
}

// Sync fuerza la persistencia del archivo activo. Implementa zapcore.WriteSyncer.
func (w *dailyWriter) Sync() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.file == nil {
		return nil
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sincronizando archivo diario de logs: %w", err)
	}
	return nil
}

// releaseRegistration libera la ruta reservada por este escritor.
func (w *dailyWriter) releaseRegistration() {
	if w.registryKey == "" {
		return
	}
	activeDailyWriters.Lock()
	delete(activeDailyWriters.paths, w.registryKey)
	activeDailyWriters.Unlock()
	w.registryKey = ""
}

// openDate cambia el escritor al segmento disponible de la fecha indicada.
func (w *dailyWriter) openDate(date string, now time.Time) error {
	if err := w.finishCurrent(); err != nil {
		return err
	}
	index, err := w.availableSegment(date)
	if err != nil {
		return err
	}
	if w.compress {
		if err := w.compressCompletedFiles(date, index); err != nil {
			return err
		}
	}
	if err := w.openSegment(date, index); err != nil {
		return err
	}

	// La limpieza es de mejor esfuerzo para no perder el registro actual si un
	// archivo antiguo no puede eliminarse.
	_ = w.cleanup(now)
	return nil
}

// compressCompletedFiles comprime los segmentos terminados excepto el activo.
func (w *dailyWriter) compressCompletedFiles(activeDate string, activeIndex int) error {
	files, err := w.logFiles()
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.compressed {
			continue
		}
		if file.date.Format(dailyDateLayout) == activeDate && file.index == activeIndex {
			continue
		}
		if err := compressLogFile(file.path); err != nil {
			return fmt.Errorf("comprimiendo archivo de logs terminado: %w", err)
		}
	}
	return nil
}

// openNextSegment cierra el segmento actual y abre el siguiente de la misma fecha.
func (w *dailyWriter) openNextSegment(now time.Time) error {
	if err := w.finishCurrent(); err != nil {
		return err
	}
	if err := w.openSegment(w.currentDate, w.currentIndex+1); err != nil {
		return err
	}

	_ = w.cleanup(now)
	return nil
}

// finishCurrent cierra el segmento activo y lo comprime cuando corresponde.
func (w *dailyWriter) finishCurrent() error {
	if w.file == nil {
		return nil
	}

	path := w.file.Name()
	size := w.currentSize
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("cerrando archivo de logs rotado: %w", err)
	}
	w.file = nil
	w.currentSize = 0

	if w.compress && size > 0 {
		if err := compressLogFile(path); err != nil {
			return fmt.Errorf("comprimiendo archivo de logs rotado: %w", err)
		}
	}
	return nil
}

// openSegment abre o crea el segmento identificado por fecha e índice.
func (w *dailyWriter) openSegment(date string, index int) error {
	path := w.segmentPath(date, index)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("abriendo archivo diario de logs: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("consultando archivo diario de logs: %w", err)
	}

	w.file = file
	w.currentDate = date
	w.currentIndex = index
	w.currentSize = info.Size()
	return nil
}

// availableSegment determina el índice reutilizable o siguiente para una fecha.
func (w *dailyWriter) availableSegment(date string) (int, error) {
	files, err := w.logFiles()
	if err != nil {
		return 0, err
	}

	maxIndex := -1
	var active *dailyLogFile
	for i := range files {
		file := &files[i]
		if file.date.Format(dailyDateLayout) != date {
			continue
		}
		if file.index > maxIndex {
			maxIndex = file.index
			active = nil
		}
		if file.index == maxIndex && !file.compressed {
			active = file
		}
	}

	if maxIndex < 0 {
		return 0, nil
	}
	if active != nil {
		info, err := os.Stat(active.path)
		if err != nil {
			return 0, fmt.Errorf("consultando segmento activo de logs: %w", err)
		}
		if info.Size() < w.maxSizeBytes {
			return maxIndex, nil
		}
	}
	return maxIndex + 1, nil
}

// cleanup elimina archivos vencidos y respaldos que exceden la retención.
func (w *dailyWriter) cleanup(now time.Time) error {
	files, err := w.logFiles()
	if err != nil {
		return err
	}

	currentName := ""
	if w.file != nil {
		currentName = filepath.Base(w.file.Name())
	}

	remaining := make([]dailyLogFile, 0, len(files))
	var firstErr error
	for _, file := range files {
		if file.name == currentName {
			continue
		}
		if w.maxAgeDays > 0 {
			cutoff := beginningOfDay(now).AddDate(0, 0, -w.maxAgeDays)
			if file.date.Before(cutoff) {
				if err := os.Remove(file.path); err != nil && firstErr == nil {
					firstErr = fmt.Errorf("eliminando archivo de logs vencido: %w", err)
				}
				continue
			}
		}
		remaining = append(remaining, file)
	}

	if w.maxBackups > 0 && len(remaining) > w.maxBackups {
		sort.Slice(remaining, func(i, j int) bool {
			if remaining[i].date.Equal(remaining[j].date) {
				return remaining[i].index > remaining[j].index
			}
			return remaining[i].date.After(remaining[j].date)
		})
		for _, file := range remaining[w.maxBackups:] {
			if err := os.Remove(file.path); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("eliminando respaldo excedente de logs: %w", err)
			}
		}
	}
	return firstErr
}

// logFiles obtiene los archivos de este escritor y extrae sus metadatos.
func (w *dailyWriter) logFiles() ([]dailyLogFile, error) {
	entries, err := os.ReadDir(w.directory)
	if err != nil {
		return nil, fmt.Errorf("leyendo directorio de logs: %w", err)
	}

	location := time.UTC
	if w.localTime {
		location = time.Local
	}

	files := make([]dailyLogFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, index, compressed, ok := parseLogFileName(w.baseName, entry.Name(), location)
		if !ok {
			continue
		}
		files = append(files, dailyLogFile{
			name:       entry.Name(),
			path:       filepath.Join(w.directory, entry.Name()),
			date:       date,
			index:      index,
			compressed: compressed,
		})
	}
	return files, nil
}

// segmentPath construye la ruta de un segmento a partir de su fecha e índice.
func (w *dailyWriter) segmentPath(date string, index int) string {
	name := w.baseName + "-" + date
	if index > 0 {
		name += fmt.Sprintf("-%02d", index)
	}
	return filepath.Join(w.directory, name+".log")
}

// currentTime devuelve la hora actual en la zona configurada para la rotación.
func (w *dailyWriter) currentTime() time.Time {
	now := w.now()
	if w.localTime {
		return now.In(time.Local)
	}
	return now.UTC()
}

// normalizeBaseName elimina la extensión .log del nombre base cuando existe.
func normalizeBaseName(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".log") {
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return name
}

// parseLogFileName analiza la fecha, el índice y la compresión de un archivo de logs.
func parseLogFileName(baseName, name string, location *time.Location) (time.Time, int, bool, bool) {
	prefix := baseName + "-"
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, 0, false, false
	}

	value := strings.TrimPrefix(name, prefix)
	compressed := strings.HasSuffix(value, ".gz")
	if compressed {
		value = strings.TrimSuffix(value, ".gz")
	}
	if !strings.HasSuffix(value, ".log") {
		return time.Time{}, 0, false, false
	}
	value = strings.TrimSuffix(value, ".log")
	if len(value) < len(dailyDateLayout) {
		return time.Time{}, 0, false, false
	}

	date, err := time.ParseInLocation(dailyDateLayout, value[:len(dailyDateLayout)], location)
	if err != nil {
		return time.Time{}, 0, false, false
	}

	suffix := value[len(dailyDateLayout):]
	if suffix == "" {
		return date, 0, compressed, true
	}
	if !strings.HasPrefix(suffix, "-") {
		return time.Time{}, 0, false, false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(suffix, "-"))
	if err != nil || index <= 0 {
		return time.Time{}, 0, false, false
	}
	return date, index, compressed, true
}

// beginningOfDay devuelve el inicio del día en la zona horaria del valor.
func beginningOfDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

// compressLogFile comprime un archivo con gzip y conserva sus permisos y fecha.
func compressLogFile(path string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	sourceInfo, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".daily-log-*.tmp")
	if err != nil {
		_ = source.Close()
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	compressor := gzip.NewWriter(temporary)
	if _, err := io.Copy(compressor, source); err != nil {
		_ = compressor.Close()
		_ = temporary.Close()
		_ = source.Close()
		return err
	}
	if err := compressor.Close(); err != nil {
		_ = temporary.Close()
		_ = source.Close()
		return err
	}
	if err := temporary.Chmod(sourceInfo.Mode().Perm()); err != nil {
		_ = temporary.Close()
		_ = source.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		_ = source.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = source.Close()
		return err
	}
	if err := source.Close(); err != nil {
		return err
	}

	compressedPath := path + ".gz"
	if err := os.Rename(temporaryPath, compressedPath); err != nil {
		return err
	}
	removeTemporary = false
	if err := os.Chtimes(compressedPath, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}
