// File: internal/util/logger.go

package util

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARNING
	ERROR
)

var (
	logger      *log.Logger
	logLevel    LogLevel
	logMutex    sync.Mutex
	logFile     *os.File
	initialized bool
)

// InitLogger initializes the logger with the specified level and filename.
func InitLogger(level LogLevel, filename string) error {
	logMutex.Lock()
	defer logMutex.Unlock()

	if initialized {
		return fmt.Errorf("logger is already initialized")
	}

	logLevel = level

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	logger = log.New(file, "", log.LstdFlags)
	logFile = file
	initialized = true
	return nil
}

// CloseLogger closes the log file.
func CloseLogger() {
	logMutex.Lock()
	defer logMutex.Unlock()

	if initialized {
		logFile.Close()
		initialized = false
	}
}

// logMessage logs a message at the specified level.
func logMessage(level LogLevel, format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()

	if !initialized || level < logLevel {
		return
	}

	msg := fmt.Sprintf(format, args...)
	prefix := ""
	switch level {
	case DEBUG:
		prefix = "DEBUG: "
	case INFO:
		prefix = "INFO: "
	case WARNING:
		prefix = "WARNING: "
	case ERROR:
		prefix = "ERROR: "
	}
	logger.SetPrefix(prefix)
	logger.Println(msg)

	// Also print to console
	fmt.Println(prefix + msg)
}

// Debug logs a message at DEBUG level.
func Debug(format string, args ...interface{}) {
	logMessage(DEBUG, format, args...)
}

// Info logs a message at INFO level.
func Info(format string, args ...interface{}) {
	logMessage(INFO, format, args...)
}

// Warning logs a message at WARNING level.
func Warning(format string, args ...interface{}) {
	logMessage(WARNING, format, args...)
}

// Error logs a message at ERROR level.
func Error(format string, args ...interface{}) {
	logMessage(ERROR, format, args...)
}

// ParseLogLevel parses the log level from a string.
func ParseLogLevel(levelStr string) (LogLevel, error) {
	switch strings.ToLower(levelStr) {
	case "debug":
		return DEBUG, nil
	case "info":
		return INFO, nil
	case "warning":
		return WARNING, nil
	case "error":
		return ERROR, nil
	default:
		return INFO, fmt.Errorf("unknown log level: %s", levelStr)
	}
}
