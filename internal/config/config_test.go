package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetEnv(t *testing.T) {
	t.Setenv("TEST_KEY", "val")
	if v := GetEnv("TEST_KEY", "fallback"); v != "val" {
		t.Fatalf("expected val, got %s", v)
	}
	t.Setenv("TEST_KEY", "")
	if v := GetEnv("TEST_KEY", "fallback"); v != "fallback" {
		t.Fatalf("expected fallback, got %s", v)
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	if v := GetEnvInt("TEST_INT", 0); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	t.Setenv("TEST_INT", "")
	if v := GetEnvInt("TEST_INT", 10); v != 10 {
		t.Fatalf("expected 10, got %d", v)
	}
	t.Setenv("TEST_INT", "invalid")
	if v := GetEnvInt("TEST_INT", 5); v != 5 {
		t.Fatalf("expected fallback on invalid, got %d", v)
	}
}

func TestSplitEnv(t *testing.T) {
	t.Setenv("TEST_LIST", "a,b,c")
	parts := SplitEnv("TEST_LIST")
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("unexpected: %v", parts)
	}
	t.Setenv("TEST_LIST", "")
	if parts := SplitEnv("TEST_LIST"); parts != nil {
		t.Fatalf("expected nil, got %v", parts)
	}
	t.Setenv("TEST_LIST", " a , b , c ")
	parts = SplitEnv("TEST_LIST")
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("trim failed: %v", parts)
	}
}

func TestLoadEnvFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".env")
	if err := os.WriteFile(path, []byte("TEST_A=hello\nTEST_B=world\n"), 0644); err != nil {
		t.Fatalf("write env: %v", err)
	}

	LoadEnvFile(path)
	if os.Getenv("TEST_A") != "hello" || os.Getenv("TEST_B") != "world" {
		t.Fatalf("env not loaded: TEST_A=%s TEST_B=%s", os.Getenv("TEST_A"), os.Getenv("TEST_B"))
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("OPENAI_BASE_URL", "https://api.example.com/v1")
	t.Setenv("OPENAI_MODEL", "model-x")
	t.Setenv("OPENAI_API_KEYS", "k1, k2")
	t.Setenv("STREAM_TIMEOUT", "90s")
	t.Setenv("RATE_LIMIT_RPM", "12")
	t.Setenv("RATE_LIMIT_TPM", "345")
	t.Setenv("AGENT_MAX_STEP", "9")
	t.Setenv("CONTEXT_WARN_PCT", "33")
	t.Setenv("CONTEXT_COMPRESS_PCT", "44")
	t.Setenv("MAX_RECONNECT_ATTEMPTS", "5")
	t.Setenv("RAW_LOG", "1")
	t.Setenv("RAW_LOG_DIR", "raw-dir")
	t.Setenv("USAGE_DB", "1")
	t.Setenv("USAGE_DB_PATH", "usage.db")
	t.Setenv("PORT", "9999")

	cfg := Load()
	if cfg.OpenAI.APIKey != "key" || cfg.OpenAI.BaseURL != "https://api.example.com/v1" || cfg.OpenAI.Model != "model-x" {
		t.Fatalf("unexpected openai config: %+v", cfg.OpenAI)
	}
	if len(cfg.OpenAI.APIKeys) != 2 || cfg.OpenAI.APIKeys[1] != "k2" {
		t.Fatalf("unexpected api keys: %v", cfg.OpenAI.APIKeys)
	}
	if cfg.OpenAI.StreamTimeout != 90*time.Second || cfg.OpenAI.RateLimitRPM != 12 || cfg.OpenAI.RateLimitTPM != 345 {
		t.Fatalf("unexpected openai limits: %+v", cfg.OpenAI)
	}
	if cfg.Agent.MaxStep != 9 || cfg.Agent.ContextWarnPct != 33 || cfg.Agent.ContextCompressPct != 44 || cfg.Agent.MaxReconnect != 5 {
		t.Fatalf("unexpected agent config: %+v", cfg.Agent)
	}
	if !cfg.Raw.RawLog || cfg.Raw.RawLogDir != "raw-dir" || !cfg.Raw.UsageDB || cfg.Raw.UsageDBPath != "usage.db" {
		t.Fatalf("unexpected raw config: %+v", cfg.Raw)
	}
	if cfg.Server.Port != "9999" {
		t.Fatalf("unexpected port: %s", cfg.Server.Port)
	}
}
