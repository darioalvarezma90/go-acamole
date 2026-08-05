package logger

// Level identifica el nivel mínimo de registros logs que se emitirá.
type Level uint8

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// isValid indica si el nivel corresponde a uno de los valores admitidos.
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

// isValid indica si la combinación de salidas contiene únicamente valores admitidos.
func (o Output) isValid() bool {
	return o != 0 && o&^ConsoleAndFileOutput == 0

}

// includes indica si la configuración contiene la salida especificada.
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

// isValid indica si la codificación corresponde a uno de los valores admitidos.
func (e Encoding) isValid() bool {
	return e == JSONEncoding || e == TextEncoding
}
