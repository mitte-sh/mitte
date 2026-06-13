package logger

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/charmbracelet/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	mu           sync.RWMutex
	stderrLogger *log.Logger
	fileLogger   *log.Logger
	useEmojis    = true
	initialized  bool
)

func init() {
	Initialize()
}

// Initialize sets up the stderr and file loggers.
// It is safe to call multiple times; only the first call has effect.
func Initialize() {
	mu.Lock()
	defer mu.Unlock()
	if initialized {
		return
	}

	stderrLogger = log.New(os.Stderr)
	stderrLogger.SetLevel(log.InfoLevel)
	setStyles(stderrLogger, useEmojis)

	logDir := "/var/log/mitte"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		stderrLogger.Warn("Could not create log directory", "path", logDir, "err", err)
		fileLogger = nil
		initialized = true
		return
	}

	logPath := filepath.Join(logDir, "mitte.log")
	fileWriter := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10, // megabytes
		MaxAge:     14, // days
		MaxBackups: 5,
		Compress:   true,
	}

	fileLogger = log.New(fileWriter)
	fileLogger.SetLevel(log.InfoLevel)
	fileLogger.SetReportTimestamp(true)
	fileLogger.SetFormatter(log.TextFormatter)
	// File output is always plain (no emojis, no colors)
	setStyles(fileLogger, false)

	initialized = true
}

func setStyles(l *log.Logger, withEmojis bool) {
	if withEmojis {
		l.SetStyles(log.DefaultStyles())
		return
	}

	styles := log.DefaultStyles()
	styles.Levels[log.DebugLevel] = styles.Levels[log.DebugLevel].SetString("DEBUG")
	styles.Levels[log.InfoLevel] = styles.Levels[log.InfoLevel].SetString("INFO")
	styles.Levels[log.WarnLevel] = styles.Levels[log.WarnLevel].SetString("WARN")
	styles.Levels[log.ErrorLevel] = styles.Levels[log.ErrorLevel].SetString("ERROR")
	styles.Levels[log.FatalLevel] = styles.Levels[log.FatalLevel].SetString("FATAL")
	l.SetStyles(styles)
}

// SetLevel changes the minimum log level for both loggers.
func SetLevel(level log.Level) {
	mu.Lock()
	defer mu.Unlock()
	stderrLogger.SetLevel(level)
	if fileLogger != nil {
		fileLogger.SetLevel(level)
	}
}

// SetUseEmojis enables or disables emoji prefixes on stderr output.
// File output is always plain.
func SetUseEmojis(v bool) {
	mu.Lock()
	defer mu.Unlock()
	useEmojis = v
	setStyles(stderrLogger, v)
}

// Debug logs a debug message.
func Debug(msg string, keyvals ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	stderrLogger.Debug(msg, keyvals...)
	if fileLogger != nil {
		fileLogger.Debug(msg, keyvals...)
	}
}

// Info logs an informational message.
func Info(msg string, keyvals ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	stderrLogger.Info(msg, keyvals...)
	if fileLogger != nil {
		fileLogger.Info(msg, keyvals...)
	}
}

// Warn logs a warning message.
func Warn(msg string, keyvals ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	stderrLogger.Warn(msg, keyvals...)
	if fileLogger != nil {
		fileLogger.Warn(msg, keyvals...)
	}
}

// Error logs an error message.
func Error(msg string, keyvals ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	stderrLogger.Error(msg, keyvals...)
	if fileLogger != nil {
		fileLogger.Error(msg, keyvals...)
	}
}

// Fatal logs a fatal message and exits the process.
func Fatal(msg string, keyvals ...interface{}) {
	mu.RLock()
	if fileLogger != nil {
		fileLogger.Log(log.FatalLevel, msg, keyvals...)
	}
	stderrLogger.Log(log.FatalLevel, msg, keyvals...)
	mu.RUnlock()
	os.Exit(1)
}
