package logger

// Level identifica el nivel mínimo de registros logs que se emitirá.
type Level uint8

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

func (l Level) isValid() bool {
	return l == DebugLevel || l == InfoLevel || l == WarnLevel || l == ErrorLevel
}

// Output identifica las salidas a ser utilizadas por un logger.
type Output uint8

const (
	// ConsoleOutput escribe únicamente en el escritor de consola configurado.
	ConsoleOutput Output = 1 << iota

	// FileOutput escribe únicamente en un archivo con rotación.
	FileOutput

	// ConsoleAndFileOutput escribe tanto en consola como en un archivo con rotación.
	ConsoleAndFileOutput = ConsoleOutput | FileOutput
)

func (o Output) isValid() bool {
	return o != 0 && o&^ConsoleAndFileOutput == 0

}

func (o Output) includes(output Output) bool {
	return o&output != 0
}

// Encoding identifica la codificación utilizada.
type Encoding uint8

const (
	// JSONEncoding genera un objeto JSON por línea. Es la codificación
	// recomendada para contenedores y recolectores de registros.
	JSONEncoding Encoding = iota

	// TextEncoding genera un formato de texto legible.
	TextEncoding
)

func (e Encoding) isValid() bool {
	return e == JSONEncoding || e == TextEncoding
}
