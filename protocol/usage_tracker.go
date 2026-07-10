package protocol

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type UsageRecord struct {
	ID               int64   `json:"id"`
	SessionID        string  `json:"session_id"`
	Model            string  `json:"model"`
	Vendor           string  `json:"vendor"`
	Duration         float64 `json:"duration"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CreatedAt        string  `json:"created_at"`
}

type UsageTracker struct {
	db *sql.DB
	mu sync.Mutex
}

func NewUsageTracker(dbPath string) (*UsageTracker, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS usage_records (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id      TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			vendor          TEXT NOT NULL DEFAULT '',
			duration        REAL NOT NULL DEFAULT 0,
			prompt_tokens   INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens    INTEGER NOT NULL DEFAULT 0,
			created_at      TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_usage_session_id ON usage_records(session_id)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create index: %w", err)
	}
	return &UsageTracker{db: db}, nil
}

func (ut *UsageTracker) Record(sessionID, model, vendor string, durSec float64, promptTks, completionTks int) error {
	total := promptTks + completionTks
	now := time.Now().UTC().Format(time.RFC3339)
	ut.mu.Lock()
	defer ut.mu.Unlock()
	_, err := ut.db.Exec(
		`INSERT INTO usage_records(session_id, model, vendor, duration, prompt_tokens, completion_tokens, total_tokens, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		sessionID, model, vendor, durSec, promptTks, completionTks, total, now,
	)
	return err
}

type UsageQuery struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Vendor    string `json:"vendor"`
	Since     string `json:"since"`
	Until     string `json:"until"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

func (ut *UsageTracker) Query(filter UsageQuery) ([]UsageRecord, error) {
	where := ""
	args := []any{}
	if filter.SessionID != "" {
		where += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}
	if filter.Model != "" {
		where += " AND model = ?"
		args = append(args, filter.Model)
	}
	if filter.Vendor != "" {
		where += " AND vendor = ?"
		args = append(args, filter.Vendor)
	}
	if filter.Since != "" {
		where += " AND created_at >= ?"
		args = append(args, filter.Since)
	}
	if filter.Until != "" {
		where += " AND created_at <= ?"
		args = append(args, filter.Until)
	}
	if where != "" {
		where = " WHERE " + where[5:]
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	q := fmt.Sprintf("SELECT id, session_id, model, vendor, duration, prompt_tokens, completion_tokens, total_tokens, created_at FROM usage_records%s ORDER BY id DESC LIMIT ? OFFSET ?", where)
	args = append(args, limit, offset)
	ut.mu.Lock()
	rows, err := ut.db.Query(q, args...)
	ut.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []UsageRecord
	for rows.Next() {
		var r UsageRecord
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Model, &r.Vendor, &r.Duration, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

type UsageStats struct {
	TotalCalls   int     `json:"total_calls"`
	AvgDuration  float64 `json:"avg_duration"`
	TotalTokens  int     `json:"total_tokens"`
	TotalPrompt  int     `json:"total_prompt"`
	TotalCompletion int  `json:"total_completion"`
}

func (ut *UsageTracker) GetStats() (UsageStats, error) {
	ut.mu.Lock()
	row := ut.db.QueryRow(`SELECT COUNT(*), COALESCE(AVG(duration),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0) FROM usage_records`)
	ut.mu.Unlock()
	var s UsageStats
	err := row.Scan(&s.TotalCalls, &s.AvgDuration, &s.TotalTokens, &s.TotalPrompt, &s.TotalCompletion)
	return s, err
}

func (ut *UsageTracker) Close() error {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	return ut.db.Close()
}
