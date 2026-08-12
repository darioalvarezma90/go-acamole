package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoggerWritesAllLevels(t *testing.T) {
	var output bytes.Buffer
	log, err := NewLogger("service",
		WithConsoleWriter(&output),
		WithLevel(DebugLevel),
	)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	log.Debug("debug", "request_id", 1)
	log.Info("info", "request_id", 2)
	log.Warn("warn", "request_id", 3)
	log.Error("error", "request_id", 4)

	records := decodeJSONRecords(t, output.String())
	if len(records) != 4 {
		t.Fatalf("record count = %d, want 4; output=%q", len(records), output.String())
	}

	wantLevels := []string{"debug", "info", "warn", "error"}
	for i, record := range records {
		if record["app"] != "service" {
			t.Errorf("record %d app = %v, want service", i, record["app"])
		}
		if record["level"] != wantLevels[i] {
			t.Errorf("record %d level = %v, want %s", i, record["level"], wantLevels[i])
		}
		if record["request_id"] != float64(i+1) {
			t.Errorf("record %d request_id = %v, want %d", i, record["request_id"], i+1)
		}
	}
}

func TestLoggerDefaults(t *testing.T) {
	var output bytes.Buffer
	log, err := NewLogger("", WithConsoleWriter(&output))
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	log.Debug("debug visible")
	log.Info("info visible")

	records := decodeJSONRecords(t, output.String())
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	for index, record := range records {
		if record["app"] != defaultAppName {
			t.Errorf("record %d default app = %v, want %s", index, record["app"], defaultAppName)
		}
	}
	if records[0]["msg"] != "debug visible" || records[0]["level"] != "debug" {
		t.Errorf("first record = %#v, want debug/debug visible", records[0])
	}
	if records[1]["msg"] != "info visible" || records[1]["level"] != "info" {
		t.Errorf("second record = %#v, want info/info visible", records[1])
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	var output bytes.Buffer
	log, err := NewLogger(
		"service",
		WithConsoleWriter(&output),
		WithLevel(InfoLevel),
	)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	log.Debug("filtered")
	log.Info("visible")

	records := decodeJSONRecords(t, output.String())
	if len(records) != 1 || records[0]["msg"] != "visible" {
		t.Fatalf("unexpected filtered records: %#v", records)
	}
}

func TestLoggerTextEncoding(t *testing.T) {
	var output bytes.Buffer
	log, err := NewLogger("service",
		WithConsoleWriter(&output),
		WithEncoding(TextEncoding),
	)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	log.Info("plain text", "answer", 42)

	got := output.String()
	for _, want := range []string{"INFO", "plain text", "service", "answer", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("text output %q does not contain %q", got, want)
		}
	}
}

func TestLoggerOptionsAndRotationCopy(t *testing.T) {
	var output bytes.Buffer
	rotation, err := NewRotation(
		WithMaxSizeMB(2),
		WithMaxBackups(1),
		WithMaxAgeDays(3),
		WithCompression(),
		WithLocalTime(),
	)
	if err != nil {
		t.Fatalf("NewRotation() error = %v", err)
	}

	log, err := NewLogger("service",
		nil,
		WithOutput(ConsoleOutput),
		WithLevel(WarnLevel),
		WithEncoding(TextEncoding),
		WithFileName("custom.log"),
		WithDirectory("custom-directory"),
		WithConsoleWriter(&output),
		WithRotation(rotation),
	)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	rotation.MaxSizeMB = 99
	if log.rotation == rotation {
		t.Fatal("WithRotation retained the caller's pointer")
	}
	if log.rotation.MaxSizeMB != 2 {
		t.Errorf("copied MaxSizeMB = %d, want 2", log.rotation.MaxSizeMB)
	}
	if !log.rotation.Compress || !log.rotation.LocalTime {
		t.Errorf("copied rotation flags were not applied: %+v", log.rotation)
	}
	if log.output != ConsoleOutput || log.level != WarnLevel || log.encoding != TextEncoding {
		t.Errorf("logger options were not applied: output=%v level=%v encoding=%v", log.output, log.level, log.encoding)
	}
	if log.fileName != "custom.log" || log.directory != "custom-directory" || log.consoleWriter != &output {
		t.Error("file/directory/writer options were not applied")
	}
}

func TestLoggerConsoleAndFileOutput(t *testing.T) {
	directory := t.TempDir()
	var console bytes.Buffer
	rotation, err := NewRotation()
	if err != nil {
		t.Fatalf("NewRotation() error = %v", err)
	}

	log, err := NewLogger("service",
		WithOutput(ConsoleAndFileOutput),
		WithConsoleWriter(&console),
		WithDirectory(directory),
		WithFileName("events.log"),
		WithRotation(rotation),
	)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	log.Info("written twice", "id", 7)
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	consoleRecords := decodeJSONRecords(t, console.String())
	if len(consoleRecords) != 1 || consoleRecords[0]["msg"] != "written twice" {
		t.Fatalf("unexpected console records: %#v", consoleRecords)
	}

	paths, err := filepath.Glob(filepath.Join(directory, "events-*.log"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("log files = %v, want one file", paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	fileRecords := decodeJSONRecords(t, string(data))
	if len(fileRecords) != 1 || fileRecords[0]["id"] != float64(7) {
		t.Fatalf("unexpected file records: %#v", fileRecords)
	}
	if strings.Contains(filepath.Base(paths[0]), ".log.log") {
		t.Fatalf("file name contains duplicate extension: %s", paths[0])
	}
}

func TestNewLoggerRejectsInvalidConfiguration(t *testing.T) {
	directory := t.TempDir()
	validRotation, err := NewRotation()
	if err != nil {
		t.Fatalf("NewRotation() error = %v", err)
	}

	tests := []struct {
		name        string
		appName     string
		options     []LoggerOption
		wantInError string
	}{
		{name: "blank app", appName: "   ", wantInError: "nombre app"},
		{name: "padded app", appName: " app", wantInError: "espacios"},
		{name: "invalid output", appName: "app", options: []LoggerOption{WithOutput(Output(0))}, wantInError: "output"},
		{name: "invalid level", appName: "app", options: []LoggerOption{WithLevel(Level(255))}, wantInError: "nivel"},
		{name: "invalid encoding", appName: "app", options: []LoggerOption{WithEncoding(Encoding(255))}, wantInError: "codificacion"},
		{name: "nil console writer", appName: "app", options: []LoggerOption{WithConsoleWriter(nil)}, wantInError: "writer"},
		{name: "blank directory", appName: "app", options: []LoggerOption{WithOutput(FileOutput), WithDirectory(" "), WithRotation(validRotation)}, wantInError: "directorio"},
		{name: "file name with directory", appName: "app", options: []LoggerOption{WithOutput(FileOutput), WithDirectory(directory), WithFileName(filepath.Join("nested", "app")), WithRotation(validRotation)}, wantInError: "directorios"},
		{name: "empty normalized file name", appName: "app", options: []LoggerOption{WithOutput(FileOutput), WithDirectory(directory), WithFileName(".log"), WithRotation(validRotation)}, wantInError: "archivo"},
		{name: "nil rotation", appName: "app", options: []LoggerOption{WithOutput(FileOutput), WithDirectory(directory), WithRotation(nil)}, wantInError: "rotación"},
		{name: "invalid rotation", appName: "app", options: []LoggerOption{WithOutput(FileOutput), WithDirectory(directory), WithRotation(&Rotation{MaxSizeMB: 0})}, wantInError: "mayor que cero"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLogger(test.appName, test.options...)
			if err == nil {
				t.Fatal("NewLogger() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("NewLogger() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}

	var nilLogger *Logger
	if err := nilLogger.validate(); err == nil {
		t.Error("nil Logger.validate() error = nil")
	}
}

func TestLoggerCloseIsIdempotentAndStopsLogging(t *testing.T) {
	var output bytes.Buffer
	log, err := NewLogger("service", WithConsoleWriter(&output))
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	log.Info("before close")
	if err := log.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	log.Info("after close")

	records := decodeJSONRecords(t, output.String())
	if len(records) != 1 || records[0]["msg"] != "before close" {
		t.Fatalf("unexpected records after close: %#v", records)
	}

	var nilLogger *Logger
	if err := nilLogger.Close(); err != nil {
		t.Errorf("nil Logger.Close() error = %v", err)
	}
	if err := nilLogger.Sync(); err != nil {
		t.Errorf("nil Logger.Sync() error = %v", err)
	}
}

func TestLoggerExposesMaintenanceError(t *testing.T) {
	log := &Logger{fileWriter: &dailyWriter{}}
	want := errors.New("retention failed")
	log.fileWriter.recordMaintenanceError(want)

	if !errors.Is(log.MaintenanceError(), want) {
		t.Fatalf("MaintenanceError() = %v, want %v", log.MaintenanceError(), want)
	}
	var nilLogger *Logger
	if nilLogger.MaintenanceError() != nil {
		t.Fatal("nil Logger.MaintenanceError() != nil")
	}
}

func TestLoggerConcurrentWritesAndClose(t *testing.T) {
	var output bytes.Buffer
	log, err := NewLogger("service",
		WithConsoleWriter(&output),
		WithLevel(DebugLevel),
	)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	start := make(chan struct{})
	firstWriteDone := make(chan struct{}, 12)
	continueWriting := make(chan struct{})
	var writers sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		writers.Add(1)
		go func(worker int) {
			defer writers.Done()
			<-start
			log.Debug("concurrent", "worker", worker, "item", 0)
			firstWriteDone <- struct{}{}
			<-continueWriting
			for item := 1; item < 50; item++ {
				log.Debug("concurrent", "worker", worker, "item", item)
			}
		}(worker)
	}

	close(start)
	for i := 0; i < 12; i++ {
		<-firstWriteDone
	}
	close(continueWriting)
	var closers sync.WaitGroup
	for i := 0; i < 4; i++ {
		closers.Add(1)
		go func() {
			defer closers.Done()
			_ = log.Close()
		}()
	}
	writers.Wait()
	closers.Wait()

	if !log.closed.Load() {
		t.Error("logger was not marked closed")
	}
	records := decodeJSONRecords(t, output.String())
	if len(records) < 12 || len(records) > 600 {
		t.Fatalf("concurrent record count = %d, want between 12 and 600", len(records))
	}
	for i, record := range records {
		if record["msg"] != "concurrent" {
			t.Errorf("record %d message = %v, want concurrent", i, record["msg"])
		}
	}
}

func TestLoggerCloseCachesFileError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed-log-*.log")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("precondition Close() error = %v", err)
	}

	log := &Logger{fileWriter: &dailyWriter{file: file}}
	firstErr := log.Close()
	if firstErr == nil {
		t.Fatal("first Logger.Close() error = nil for an already-closed file")
	}
	secondErr := log.Close()
	if secondErr != firstErr {
		t.Fatalf("second Logger.Close() error = %v, want cached error %v", secondErr, firstErr)
	}
}

func decodeJSONRecords(t *testing.T, output string) []map[string]any {
	t.Helper()

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	records := make([]map[string]any, 0, len(lines))
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not valid JSON: %v; line=%q", index, err, line)
		}
		records = append(records, record)
	}
	return records
}
