package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
	FATAL: "FATAL",
}

type fileWriter struct {
	dir      string
	file     *os.File
	mu       sync.Mutex
	currDate string
}

func (w *fileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	if today != w.currDate {
		w.rotate(today)
	}
	return w.file.Write(p)
}

func (w *fileWriter) rotate(date string) {
	if w.file != nil {
		w.file.Close()
	}
	os.MkdirAll(w.dir, 0755)
	path := filepath.Join(w.dir, date+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	w.file = f
	w.currDate = date
}

type Logger struct {
	level   Level
	writers []io.Writer
	mu      sync.Mutex
}

var defaultLogger = &Logger{
	level:   INFO,
	writers: []io.Writer{os.Stdout},
}

func init() {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		defaultLogger.level = DEBUG
	case "info":
		defaultLogger.level = INFO
	case "warn":
		defaultLogger.level = WARN
	case "error":
		defaultLogger.level = ERROR
	}
	if dir := os.Getenv("LOG_DIR"); dir != "" {
		fw := &fileWriter{dir: dir}
		fw.rotate(time.Now().Format("2006-01-02"))
		defaultLogger.AddWriter(fw)
	}
}

func SetLevel(l Level) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = l
}

func (l *Logger) AddWriter(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writers = append(l.writers, w)
}

func output(lvl Level, format string, args ...any) {
	if lvl < defaultLogger.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	_, file, line, ok := runtime.Caller(2)
	short := ""
	if ok {
		short = filepath.Base(file)
	}
	timestamp := time.Now().Format("15:04:05.000")
	name := levelNames[lvl]
	var prefix string
	if short != "" {
		prefix = fmt.Sprintf("%s [%s] %s:%d ", timestamp, name, short, line)
	} else {
		prefix = fmt.Sprintf("%s [%s] ", timestamp, name)
	}
	lineOut := prefix + msg + "\n"

	defaultLogger.mu.Lock()
	writers := make([]io.Writer, len(defaultLogger.writers))
	copy(writers, defaultLogger.writers)
	defaultLogger.mu.Unlock()

	for _, w := range writers {
		w.Write([]byte(lineOut))
	}

	if lvl == FATAL {
		os.Exit(1)
	}
}

func Debug(format string, args ...any) { output(DEBUG, format, args...) }
func Info(format string, args ...any)  { output(INFO, format, args...) }
func Warn(format string, args ...any)  { output(WARN, format, args...) }
func Error(format string, args ...any) { output(ERROR, format, args...) }
func Fatal(format string, args ...any) { output(FATAL, format, args...) }
