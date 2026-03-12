package correlation

import "github.com/rs/zerolog"

// testLogger returns a discarded logger suitable for unit tests.
func testLogger() zerolog.Logger {
	return zerolog.Nop()
}
