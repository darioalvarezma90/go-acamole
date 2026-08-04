package logger

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewDailyWriterRegistersUniqueTarget(t *testing.T) {
	directory := t.TempDir()
	rotation, err := NewRotation(WithCompression(false))
	if err != nil {
		t.Fatalf("NewRotation() error = %v", err)
	}

	first, err := newDailyWriter(directory, "application.log", rotation)
	if err != nil {
		t.Fatalf("first newDailyWriter() error = %v", err)
	}
	if first.baseName != "application" {
		t.Errorf("baseName = %q, want application", first.baseName)
	}
	if first.maxSizeBytes != int64(rotation.MaxSizeMB)*bytesPerMB {
		t.Errorf("maxSizeBytes = %d, want %d", first.maxSizeBytes, int64(rotation.MaxSizeMB)*bytesPerMB)
	}

	if _, err := newDailyWriter(directory, "application", rotation); err == nil {
		t.Fatal("duplicate newDailyWriter() error = nil")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := newDailyWriter(directory, "application", rotation)
	if err != nil {
		t.Fatalf("newDailyWriter() after Close error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestDailyWriterWriteSyncAndClose(t *testing.T) {
	now := time.Date(2026, time.August, 3, 14, 30, 0, 0, time.UTC)
	writer := newInMemoryConfiguredWriter(t, now, 100, false)

	written, err := writer.Write([]byte("first record\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len("first record\n") {
		t.Errorf("Write() bytes = %d, want %d", written, len("first record\n"))
	}
	if writer.currentDate != "03-08-2026" || writer.currentIndex != 0 {
		t.Errorf("active segment = %s/%d, want 03-08-2026/0", writer.currentDate, writer.currentIndex)
	}
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	path := writer.segmentPath("03-08-2026", 0)
	assertFileContent(t, path, "first record\n")
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := writer.Write([]byte("after close")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Write() after Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync() after Close error = %v", err)
	}
}

func TestDailyWriterRotatesBySize(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	writer := newInMemoryConfiguredWriter(t, now, 5, false)
	t.Cleanup(func() { _ = writer.Close() })

	if _, err := writer.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if _, err := writer.Write([]byte("56")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}

	assertFileContent(t, writer.segmentPath("03-08-2026", 0), "1234")
	assertFileContent(t, writer.segmentPath("03-08-2026", 1), "56")
	if writer.currentIndex != 1 {
		t.Errorf("currentIndex = %d, want 1", writer.currentIndex)
	}
}

func TestDailyWriterConcurrentWrites(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	writer := newInMemoryConfiguredWriter(t, now, 1<<20, false)

	const workers = 10
	const recordsPerWorker = 40
	var waitGroup sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for record := 0; record < recordsPerWorker; record++ {
				if _, err := fmt.Fprintf(writer, "%d-%d\n", worker, record); err != nil {
					t.Errorf("Write() error = %v", err)
					return
				}
			}
		}(worker)
	}
	waitGroup.Wait()
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(writer.segmentPath("03-08-2026", 0))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != workers*recordsPerWorker {
		t.Fatalf("line count = %d, want %d", len(lines), workers*recordsPerWorker)
	}
}

func TestDailyWriterCompressesCompletedSegment(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	writer := newInMemoryConfiguredWriter(t, now, 5, true)
	t.Cleanup(func() { _ = writer.Close() })

	if _, err := writer.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if _, err := writer.Write([]byte("56")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}

	firstPath := writer.segmentPath("03-08-2026", 0)
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncompressed completed segment still exists: %v", err)
	}
	if got := readGzipFile(t, firstPath+".gz"); got != "1234" {
		t.Errorf("compressed content = %q, want 1234", got)
	}
	assertFileContent(t, writer.segmentPath("03-08-2026", 1), "56")
}

func TestDailyWriterRotatesWhenDateChanges(t *testing.T) {
	current := time.Date(2026, time.August, 3, 23, 59, 0, 0, time.UTC)
	writer := newInMemoryConfiguredWriter(t, current, 100, false)
	writer.now = func() time.Time { return current }
	t.Cleanup(func() { _ = writer.Close() })

	if _, err := writer.Write([]byte("day one")); err != nil {
		t.Fatalf("day one Write() error = %v", err)
	}
	current = current.Add(2 * time.Minute)
	if _, err := writer.Write([]byte("day two")); err != nil {
		t.Fatalf("day two Write() error = %v", err)
	}

	assertFileContent(t, writer.segmentPath("03-08-2026", 0), "day one")
	assertFileContent(t, writer.segmentPath("04-08-2026", 0), "day two")
}

func TestDailyWriterOpenDateCompressesOlderFiles(t *testing.T) {
	directory := t.TempDir()
	oldPath := filepath.Join(directory, "app-02-08-2026.log")
	activePath := filepath.Join(directory, "app-03-08-2026.log")
	writeTestFile(t, oldPath, "old")
	writeTestFile(t, activePath, "active")

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	writer := &dailyWriter{
		directory:    directory,
		baseName:     "app",
		maxSizeBytes: 100,
		compress:     true,
		now:          func() time.Time { return now },
	}
	t.Cleanup(func() { _ = writer.Close() })

	if err := writer.openDate("03-08-2026", now); err != nil {
		t.Fatalf("openDate() error = %v", err)
	}
	if got := readGzipFile(t, oldPath+".gz"); got != "old" {
		t.Errorf("old compressed content = %q, want old", got)
	}
	assertFileContent(t, activePath, "active")
	if writer.currentIndex != 0 || writer.currentSize != int64(len("active")) {
		t.Errorf("active state index/size = %d/%d, want 0/%d", writer.currentIndex, writer.currentSize, len("active"))
	}
}

func TestDailyWriterAvailableSegment(t *testing.T) {
	const date = "03-08-2026"
	tests := []struct {
		name  string
		files map[string]string
		want  int
	}{
		{name: "no files", want: 0},
		{name: "append partial", files: map[string]string{"app-03-08-2026.log": "123"}, want: 0},
		{name: "advance full", files: map[string]string{"app-03-08-2026.log": "12345"}, want: 1},
		{name: "advance compressed", files: map[string]string{"app-03-08-2026.log.gz": "gzip placeholder"}, want: 1},
		{name: "append highest partial", files: map[string]string{"app-03-08-2026.log.gz": "x", "app-03-08-2026-02.log": "1"}, want: 2},
		{name: "advance highest compressed", files: map[string]string{"app-03-08-2026-02.log.gz": "x"}, want: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			for name, content := range test.files {
				writeTestFile(t, filepath.Join(directory, name), content)
			}
			writer := &dailyWriter{directory: directory, baseName: "app", maxSizeBytes: 5}
			got, err := writer.availableSegment(date)
			if err != nil {
				t.Fatalf("availableSegment() error = %v", err)
			}
			if got != test.want {
				t.Errorf("availableSegment() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestDailyWriterCleanupByAge(t *testing.T) {
	directory := t.TempDir()
	files := []string{
		"app-31-07-2026.log",
		"app-01-08-2026.log",
		"app-03-08-2026.log",
		"unrelated.txt",
	}
	for _, name := range files {
		writeTestFile(t, filepath.Join(directory, name), name)
	}

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	writer := &dailyWriter{directory: directory, baseName: "app", maxAgeDays: 2}
	if err := writer.cleanup(now); err != nil {
		t.Fatalf("cleanup() error = %v", err)
	}

	assertNotExists(t, filepath.Join(directory, "app-31-07-2026.log"))
	assertExists(t, filepath.Join(directory, "app-01-08-2026.log"))
	assertExists(t, filepath.Join(directory, "app-03-08-2026.log"))
	assertExists(t, filepath.Join(directory, "unrelated.txt"))
}

func TestDailyWriterCleanupByBackupCount(t *testing.T) {
	directory := t.TempDir()
	files := []string{
		"app-01-08-2026.log",
		"app-02-08-2026.log",
		"app-02-08-2026-01.log",
		"app-03-08-2026.log",
	}
	for _, name := range files {
		writeTestFile(t, filepath.Join(directory, name), name)
	}

	writer := &dailyWriter{directory: directory, baseName: "app", maxBackups: 2}
	if err := writer.cleanup(time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("cleanup() error = %v", err)
	}

	assertExists(t, filepath.Join(directory, "app-03-08-2026.log"))
	assertExists(t, filepath.Join(directory, "app-02-08-2026-01.log"))
	assertNotExists(t, filepath.Join(directory, "app-02-08-2026.log"))
	assertNotExists(t, filepath.Join(directory, "app-01-08-2026.log"))
}

func TestDailyWriterLogFiles(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"app-03-08-2026.log",
		"app-03-08-2026-01.log.gz",
		"other-03-08-2026.log",
		"app-invalid.log",
	} {
		writeTestFile(t, filepath.Join(directory, name), name)
	}

	writer := &dailyWriter{directory: directory, baseName: "app"}
	files, err := writer.logFiles()
	if err != nil {
		t.Fatalf("logFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("logFiles() count = %d, want 2: %#v", len(files), files)
	}
}

func TestParseLogFileName(t *testing.T) {
	tests := []struct {
		name       string
		fileName   string
		wantOK     bool
		wantIndex  int
		compressed bool
	}{
		{name: "base", fileName: "app-03-08-2026.log", wantOK: true},
		{name: "indexed", fileName: "app-03-08-2026-12.log", wantOK: true, wantIndex: 12},
		{name: "compressed", fileName: "app-03-08-2026-01.log.gz", wantOK: true, wantIndex: 1, compressed: true},
		{name: "wrong base", fileName: "other-03-08-2026.log"},
		{name: "invalid date", fileName: "app-31-02-2026.log"},
		{name: "missing extension", fileName: "app-03-08-2026"},
		{name: "zero index", fileName: "app-03-08-2026-00.log"},
		{name: "negative index", fileName: "app-03-08-2026--1.log"},
		{name: "text index", fileName: "app-03-08-2026-one.log"},
		{name: "short", fileName: "app-03.log"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			date, index, compressed, ok := parseLogFileName("app", test.fileName, time.UTC)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if !test.wantOK {
				return
			}
			if date.Format(dailyDateLayout) != "03-08-2026" {
				t.Errorf("date = %v, want 03-08-2026", date)
			}
			if index != test.wantIndex || compressed != test.compressed {
				t.Errorf("index/compressed = %d/%v, want %d/%v", index, compressed, test.wantIndex, test.compressed)
			}
		})
	}
}

func TestDailyWriterTimeHelpers(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.FixedZone("test-zone", -6*60*60)
	t.Cleanup(func() { time.Local = previousLocal })

	instant := time.Date(2026, time.August, 3, 3, 0, 0, 0, time.UTC)
	utcWriter := &dailyWriter{now: func() time.Time { return instant }}
	localWriter := &dailyWriter{localTime: true, now: func() time.Time { return instant }}

	if got := utcWriter.currentTime(); got.Location() != time.UTC || got.Hour() != 3 {
		t.Errorf("UTC currentTime() = %v", got)
	}
	if got := localWriter.currentTime(); got.Location() != time.Local || got.Day() != 2 || got.Hour() != 21 {
		t.Errorf("local currentTime() = %v, want previous day at 21:00", got)
	}
	if got := beginningOfDay(instant); got.Hour() != 0 || got.Minute() != 0 || got.Location() != time.UTC {
		t.Errorf("beginningOfDay() = %v", got)
	}
}

func TestNameAndPathHelpers(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "app", want: "app"},
		{input: "app.log", want: "app"},
		{input: "app.LOG", want: "app"},
		{input: "app.txt", want: "app.txt"},
	} {
		if got := normalizeBaseName(test.input); got != test.want {
			t.Errorf("normalizeBaseName(%q) = %q, want %q", test.input, got, test.want)
		}
	}

	writer := &dailyWriter{directory: "logs", baseName: "app"}
	if got := writer.segmentPath("03-08-2026", 0); got != filepath.Join("logs", "app-03-08-2026.log") {
		t.Errorf("segmentPath(index 0) = %q", got)
	}
	if got := writer.segmentPath("03-08-2026", 2); got != filepath.Join("logs", "app-03-08-2026-02.log") {
		t.Errorf("segmentPath(index 2) = %q", got)
	}
}

func TestCompressLogFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writeTestFile(t, path, strings.Repeat("compressible content\n", 20))

	if err := compressLogFile(path); err != nil {
		t.Fatalf("compressLogFile() error = %v", err)
	}
	assertNotExists(t, path)
	if got := readGzipFile(t, path+".gz"); got != strings.Repeat("compressible content\n", 20) {
		t.Error("decompressed content differs from source")
	}

	if err := compressLogFile(filepath.Join(t.TempDir(), "missing.log")); err == nil {
		t.Error("compressLogFile(missing) error = nil")
	}
}

func TestDailyWriterReportsFilesystemErrors(t *testing.T) {
	missingDirectory := filepath.Join(t.TempDir(), "missing")
	writer := &dailyWriter{directory: missingDirectory, baseName: "app", maxSizeBytes: 10}

	if _, err := writer.logFiles(); err == nil {
		t.Error("logFiles() for missing directory error = nil")
	}
	if err := writer.openSegment("03-08-2026", 0); err == nil {
		t.Error("openSegment() for missing directory error = nil")
	}
}

func newInMemoryConfiguredWriter(t *testing.T, now time.Time, maxSizeBytes int64, compress bool) *dailyWriter {
	t.Helper()
	return &dailyWriter{
		directory:    t.TempDir(),
		baseName:     "app",
		maxSizeBytes: maxSizeBytes,
		maxBackups:   0,
		maxAgeDays:   0,
		compress:     compress,
		now:          func() time.Time { return now },
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if got := string(data); got != want {
		t.Errorf("file %q content = %q, want %q", path, got, want)
	}
}

func readGzipFile(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	defer func() { _ = file.Close() }()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader(%q) error = %v", path, err)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", path, err)
	}
	return string(data)
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %q to exist: %v", path, err)
	}
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q not to exist; stat error = %v", path, err)
	}
}
