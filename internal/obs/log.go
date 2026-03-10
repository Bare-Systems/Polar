package obs

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

// Level represents a log severity level.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Logger writes structured JSON log entries.
type Logger struct {
	out io.Writer
}

// Default is the process-wide logger writing to stderr.
var Default = &Logger{out: os.Stderr}

// NewLogger returns a Logger writing to w.
func NewLogger(w io.Writer) *Logger {
	return &Logger{out: w}
}

type entry struct {
	Time    string         `json:"time"`
	Level   Level          `json:"level"`
	Message string         `json:"msg"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func (l *Logger) log(level Level, msg string, fields map[string]any) {
	e := entry{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Level:   level,
		Message: msg,
		Fields:  fields,
	}
	b, _ := json.Marshal(e)
	b = append(b, '\n')
	_, _ = l.out.Write(b)
}

// Info logs an informational message with optional key-value pairs.
func (l *Logger) Info(msg string, kv ...any) {
	l.log(LevelInfo, msg, kvToMap(kv))
}

// Warn logs a warning message with optional key-value pairs.
func (l *Logger) Warn(msg string, kv ...any) {
	l.log(LevelWarn, msg, kvToMap(kv))
}

// Error logs an error message with optional key-value pairs.
func (l *Logger) Error(msg string, kv ...any) {
	l.log(LevelError, msg, kvToMap(kv))
}

// kvToMap converts a flat key-value slice into a map.
// Keys must be strings; unpaired values are dropped.
func kvToMap(kv []any) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			continue
		}
		m[k] = kv[i+1]
	}
	return m
}
