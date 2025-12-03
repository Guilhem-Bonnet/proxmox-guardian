package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents log level
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel parses a log level string
func ParseLevel(s string) Level {
	switch s {
	case "debug", "DEBUG":
		return LevelDebug
	case "info", "INFO":
		return LevelInfo
	case "warn", "WARN", "warning", "WARNING":
		return LevelWarn
	case "error", "ERROR":
		return LevelError
	default:
		return LevelInfo
	}
}

// Logger is a structured logger
type Logger struct {
	level  Level
	format string // "json" or "text"
	output io.Writer
	mu     sync.Mutex
	fields map[string]interface{}
}

// Config holds logger configuration
type Config struct {
	Level  string
	Format string
	Output io.Writer
}

// New creates a new logger
func New(cfg Config) *Logger {
	output := cfg.Output
	if output == nil {
		output = os.Stdout
	}

	format := cfg.Format
	if format == "" {
		format = "json"
	}

	return &Logger{
		level:  ParseLevel(cfg.Level),
		format: format,
		output: output,
		fields: make(map[string]interface{}),
	}
}

// WithField returns a new logger with an additional field
func (l *Logger) WithField(key string, value interface{}) *Logger {
	newLogger := &Logger{
		level:  l.level,
		format: l.format,
		output: l.output,
		fields: make(map[string]interface{}),
	}

	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	newLogger.fields[key] = value

	return newLogger
}

// WithFields returns a new logger with additional fields
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	newLogger := &Logger{
		level:  l.level,
		format: l.format,
		output: l.output,
		fields: make(map[string]interface{}),
	}

	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	for k, v := range fields {
		newLogger.fields[k] = v
	}

	return newLogger
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, keyvals ...interface{}) {
	l.log(LevelDebug, msg, keyvals...)
}

// Info logs an info message
func (l *Logger) Info(msg string, keyvals ...interface{}) {
	l.log(LevelInfo, msg, keyvals...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, keyvals ...interface{}) {
	l.log(LevelWarn, msg, keyvals...)
}

// Error logs an error message
func (l *Logger) Error(msg string, keyvals ...interface{}) {
	l.log(LevelError, msg, keyvals...)
}

func (l *Logger) log(level Level, msg string, keyvals ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Build fields from keyvals
	fields := make(map[string]interface{})
	for k, v := range l.fields {
		fields[k] = v
	}

	for i := 0; i < len(keyvals)-1; i += 2 {
		if key, ok := keyvals[i].(string); ok {
			fields[key] = keyvals[i+1]
		}
	}

	if l.format == "json" {
		l.logJSON(level, msg, fields)
	} else {
		l.logText(level, msg, fields)
	}
}

func (l *Logger) logJSON(level Level, msg string, fields map[string]interface{}) {
	entry := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"level":     level.String(),
		"message":   msg,
	}

	for k, v := range fields {
		entry[k] = v
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	fmt.Fprintln(l.output, string(data))
}

func (l *Logger) logText(level Level, msg string, fields map[string]interface{}) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	line := fmt.Sprintf("%s [%s] %s", timestamp, level.String(), msg)

	for k, v := range fields {
		line += fmt.Sprintf(" %s=%v", k, v)
	}

	fmt.Fprintln(l.output, line)
}

// DefaultLogger is the default logger instance
var DefaultLogger = New(Config{Level: "info", Format: "json"})

// SetDefault sets the default logger
func SetDefault(l *Logger) {
	DefaultLogger = l
}

// Debug logs to default logger
func Debug(msg string, keyvals ...interface{}) {
	DefaultLogger.Debug(msg, keyvals...)
}

// Info logs to default logger
func Info(msg string, keyvals ...interface{}) {
	DefaultLogger.Info(msg, keyvals...)
}

// Warn logs to default logger
func Warn(msg string, keyvals ...interface{}) {
	DefaultLogger.Warn(msg, keyvals...)
}

// Error logs to default logger
func Error(msg string, keyvals ...interface{}) {
	DefaultLogger.Error(msg, keyvals...)
}
