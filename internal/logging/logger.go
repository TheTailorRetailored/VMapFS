package logging

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// LogLevel represents different logging levels
type LogLevel int

const (
	// LevelError only logs errors
	LevelError LogLevel = iota
	// LevelWarn logs warnings and errors
	LevelWarn
	// LevelInfo logs general information, warnings and errors
	LevelInfo
	// LevelDebug logs detailed debug information and all above
	LevelDebug
	// LevelTrace logs very detailed trace information and all above
	LevelTrace
)

var levelNames = map[LogLevel]string{
	LevelError: "ERROR",
	LevelWarn:  "WARN",
	LevelInfo:  "INFO",
	LevelDebug: "DEBUG",
	LevelTrace: "TRACE",
}

// Logger provides structured logging capabilities
type Logger struct {
	level  LogLevel
	prefix string
	logger *log.Logger
	mu     sync.RWMutex
}

var (
	defaultLogger *Logger
	once          sync.Once
)

// GetLogger returns the default logger instance
func GetLogger() *Logger {
	once.Do(func() {
		defaultLogger = NewLogger("VMAPFS")

		// Set initial log level from environment
		if level := os.Getenv("LOG_LEVEL"); level != "" {
			switch level {
			case "ERROR":
				defaultLogger.SetLevel(LevelError)
			case "WARN":
				defaultLogger.SetLevel(LevelWarn)
			case "INFO":
				defaultLogger.SetLevel(LevelInfo)
			case "DEBUG":
				defaultLogger.SetLevel(LevelDebug)
			case "TRACE":
				defaultLogger.SetLevel(LevelTrace)
			}
		}

		// Enable debug logging if FUSE_DEBUG is set
		if os.Getenv("FUSE_DEBUG") != "" {
			defaultLogger.SetLevel(LevelDebug)
		}
	})
	return defaultLogger
}

// NewLogger creates a new logger with the given prefix
func NewLogger(prefix string) *Logger {
	flags := log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC
	if os.Getenv("LOG_LONGFILE") != "" {
		flags |= log.Llongfile
	} else {
		flags |= log.Lshortfile
	}

	return &Logger{
		level:  LevelInfo, // Default to INFO level
		prefix: prefix,
		logger: log.New(os.Stdout, prefix+": ", flags),
	}
}

// SetLevel sets the logging level
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// shouldLog determines if a message at the given level should be logged
func (l *Logger) shouldLog(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level <= l.level
}

// log performs the actual logging
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if !l.shouldLog(level) {
		return
	}

	msg := fmt.Sprintf(format, args...)
	if err := l.logger.Output(3, fmt.Sprintf("[%s] %s", levelNames[level], msg)); err != nil {
		// write directly to stderr
		fmt.Fprintf(os.Stderr, "Failed to write log message: %v\n", err)
	}
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Trace logs a trace message
func (l *Logger) Trace(format string, args ...interface{}) {
	l.log(LevelTrace, format, args...)
}

// WithPrefix creates a new logger with an additional prefix
func (l *Logger) WithPrefix(prefix string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := &Logger{
		logger: l.logger,
		prefix: prefix,
	}
	return newLogger
}
