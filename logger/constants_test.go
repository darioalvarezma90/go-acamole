package logger

import "testing"

func TestLevelValidation(t *testing.T) {
	tests := []struct {
		name  string
		level Level
		valid bool
	}{
		{name: "debug", level: DebugLevel, valid: true},
		{name: "info", level: InfoLevel, valid: true},
		{name: "warn", level: WarnLevel, valid: true},
		{name: "error", level: ErrorLevel, valid: true},
		{name: "unknown", level: Level(255), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.level.isValid(); got != test.valid {
				t.Fatalf("Level(%d).isValid() = %v, want %v", test.level, got, test.valid)
			}
		})
	}
}

func TestOutputValidationAndIncludes(t *testing.T) {
	tests := []struct {
		name  string
		value Output
		valid bool
	}{
		{name: "console", value: ConsoleOutput, valid: true},
		{name: "file", value: FileOutput, valid: true},
		{name: "console and file", value: ConsoleAndFileOutput, valid: true},
		{name: "empty", value: Output(0), valid: false},
		{name: "unknown bit", value: Output(1 << 7), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.value.isValid(); got != test.valid {
				t.Fatalf("Output(%d).isValid() = %v, want %v", test.value, got, test.valid)
			}
		})
	}

	if !ConsoleAndFileOutput.includes(ConsoleOutput) {
		t.Error("ConsoleAndFileOutput should include ConsoleOutput")
	}
	if !ConsoleAndFileOutput.includes(FileOutput) {
		t.Error("ConsoleAndFileOutput should include FileOutput")
	}
	if ConsoleOutput.includes(FileOutput) {
		t.Error("ConsoleOutput should not include FileOutput")
	}
}

func TestEncodingValidation(t *testing.T) {
	tests := []struct {
		name     string
		encoding Encoding
		valid    bool
	}{
		{name: "json", encoding: JSONEncoding, valid: true},
		{name: "text", encoding: TextEncoding, valid: true},
		{name: "unknown", encoding: Encoding(255), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.encoding.isValid(); got != test.valid {
				t.Fatalf("Encoding(%d).isValid() = %v, want %v", test.encoding, got, test.valid)
			}
		})
	}
}
