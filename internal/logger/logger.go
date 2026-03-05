package logger

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Category represents a log category for filtering.
type Category string

const (
	CatSecurity  Category = "security"
	CatAPI       Category = "api"
	CatBrain     Category = "brain"
	CatAgent     Category = "agent"
	CatCalendar  Category = "calendar"
	CatEmail     Category = "email"
	CatSystem    Category = "system"
	CatWebSocket Category = "websocket"
)

// AllCategories for enumeration in the UI.
var AllCategories = []Category{
	CatSecurity, CatAPI, CatBrain, CatAgent,
	CatCalendar, CatEmail, CatSystem, CatWebSocket,
}

// ring is the global ring buffer for queryable logs.
var ring *RingBuffer

// Init sets up zerolog with console (dev) or JSON (prod) output + ring buffer.
func Init(dev bool) {
	ring = NewRingBuffer(10000)

	var consoleWriter io.Writer
	if dev {
		consoleWriter = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.Kitchen}
	} else {
		consoleWriter = os.Stderr
	}

	multi := io.MultiWriter(consoleWriter, ring)
	log.Logger = zerolog.New(multi).With().Timestamp().Logger()
	zerolog.TimeFieldFormat = time.RFC3339
}

// WithCategory returns a logger with the category field set.
func WithCategory(cat Category) *zerolog.Logger {
	l := log.With().Str("category", string(cat)).Logger()
	return &l
}

// Ring returns the global ring buffer for log queries.
func Ring() *RingBuffer {
	return ring
}
