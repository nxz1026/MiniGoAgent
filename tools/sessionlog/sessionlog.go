package sessionlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"MiniGoAgent/tools/log"
)

type EntryType int

const (
	EntryUser EntryType = iota
	EntryAssistant
	EntryToolCall
	EntryToolResult
	EntrySystem
)

type Logger struct {
	sessionID string
	file      *os.File
	mu        sync.Mutex
	seq       int
}

func New(sessionID, logDir string) (*Logger, error) {
	dir := filepath.Join(logDir, "sessions", sanitize(sessionID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create session log dir: %w", err)
	}
	fname := filepath.Join(dir, time.Now().Format("2006-01-02_15-04-05")+".md")
	f, err := os.OpenFile(fname, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("create session log file: %w", err)
	}
	l := &Logger{sessionID: sessionID, file: f}
	l.writeHeader()
	return l, nil
}

func sanitize(s string) string {
	b := make([]byte, 0, len(s))
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b = append(b, c)
		} else {
			b = append(b, '_')
		}
	}
	return string(b)
}

func (l *Logger) writeHeader() {
	fmt.Fprintf(l.file, "# Session: %s\n", l.sessionID)
	fmt.Fprintf(l.file, "Started: %s\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(l.file, "| # | Time | Type | Content |\n")
	fmt.Fprintf(l.file, "|---|---|---|---|\n")
}

func (l *Logger) LogUser(content string) {
	l.log(EntryUser, content, "")
}

func (l *Logger) LogAssistant(content, stats string) {
	l.log(EntryAssistant, content, stats)
}

func (l *Logger) LogToolCall(name, args, result string) {
	l.log(EntryToolCall, "", fmt.Sprintf("**%s**\n```json\n%s\n```\n→\n```\n%s\n```", name, args, result))
}

func (l *Logger) LogToolResult(name, result string) {
	l.log(EntryToolResult, "", fmt.Sprintf("**%s** result:\n```\n%s\n```", name, result))
}

func (l *Logger) LogSystem(msg string) {
	l.log(EntrySystem, msg, "")
}

func (l *Logger) LogRaw(f string, args []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	l.seq++
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(f, args...)
	fmt.Fprintf(l.file, "| %d | %s | **RAW** | `%s` |\n", l.seq, ts, escapeMarkdown(msg))
}

func (l *Logger) log(typ EntryType, content, extra string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	l.seq++
	ts := time.Now().Format("15:04:05.000")
	var label string
	switch typ {
	case EntryUser:
		label = "User"
	case EntryAssistant:
		label = "Assistant"
	case EntryToolCall:
		label = "ToolCall"
	case EntryToolResult:
		label = "ToolResult"
	case EntrySystem:
		label = "System"
	}
	line := fmt.Sprintf("| %d | %s | **%s** | %s |\n", l.seq, ts, label, escapeMarkdown(content))
	fmt.Fprint(l.file, line)
	if extra != "" {
		fmt.Fprintf(l.file, "| | | | %s |\n", extra)
	}
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintf(l.file, "\n---\nEnded: %s\n", time.Now().Format(time.RFC3339))
		l.file.Close()
		l.file = nil
	}
}

func escapeMarkdown(s string) string {
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	// naive escaping for pipe chars
	var b []byte
	for _, c := range []byte(s) {
		if c == '|' {
			b = append(b, '\\', '|')
		} else if c == '\n' {
			b = append(b, []byte(" ... ")...)
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}

// Write assistant response entry with inline usage stats and full content saved to separate file.
func (l *Logger) WriteAssistantResponse(sessionDir, snippet, fullContent, stats string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	l.seq++
	ts := time.Now().Format("15:04:05.000")
	// write snippet with link to full content file
	contentText := snippet
	if len(fullContent) > 200 {
		contentText = snippet
		detailFile := fmt.Sprintf("response_%s_%04d.md", time.Now().Format("20060102_150405"), l.seq)
		detailPath := filepath.Join(filepath.Dir(l.file.Name()), detailFile)
		os.WriteFile(detailPath, []byte(fullContent), 0644)
		contentText = fmt.Sprintf("%s — [full response](%s)", snippet, detailFile)
	}
	line := fmt.Sprintf("| %d | %s | **Assistant** | %s |\n", l.seq, ts, escapeMarkdown(contentText))
	fmt.Fprint(l.file, line)
	if stats != "" {
		fmt.Fprintf(l.file, "| | | _stats_ | %s |\n", stats)
	}
}

func (l *Logger) LogDir() string {
	if l.file == nil {
		return ""
	}
	return filepath.Dir(l.file.Name())
}

var global struct {
	mu      sync.Mutex
	loggers map[string]*Logger
}
var defaultLogDir string

func init() {
	global.loggers = make(map[string]*Logger)
	if d := os.Getenv("SESSION_LOG_DIR"); d != "" {
		defaultLogDir = d
	} else {
		defaultLogDir = "logs"
	}
}

func Get(sessionID string) *Logger {
	global.mu.Lock()
	defer global.mu.Unlock()
	return global.loggers[sessionID]
}

func Open(sessionID string) *Logger {
	global.mu.Lock()
	defer global.mu.Unlock()
	if l, ok := global.loggers[sessionID]; ok {
		return l
	}
	l, err := New(sessionID, defaultLogDir)
	if err != nil {
		log.Warn("sessionlog open %s: %v", sessionID, err)
		return nil
	}
	global.loggers[sessionID] = l
	return l
}

func Close(sessionID string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if l, ok := global.loggers[sessionID]; ok {
		l.Close()
		delete(global.loggers, sessionID)
	}
}

func CloseAll() {
	global.mu.Lock()
	defer global.mu.Unlock()
	for id, l := range global.loggers {
		l.Close()
		delete(global.loggers, id)
	}
}
