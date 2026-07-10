package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	OpenAI   OpenAIConfig
	Server   ServerConfig
	Agent    AgentConfig
	Raw      RawConfig
	Security SecurityConfig
}

type OpenAIConfig struct {
	APIKey          string
	APIKeys         []string
	BaseURL         string
	Model           string
	FallbackModel   string
	FallbackBaseURL string
	StreamTimeout   time.Duration
	RateLimitRPM    int
	RateLimitTPM    int
}

type ServerConfig struct {
	Port string
}

type AgentConfig struct {
	MaxStep            int
	ContextWarnPct     int
	ContextCompressPct int
	MaxReconnect       int
}

type RawConfig struct {
	RawLog      bool
	RawLogDir   string
	UsageDB     bool
	UsageDBPath string
}

type SecurityConfig struct {
	AllowPrivateURLs bool
}

func Load() Config {
	LoadEnvFile(".env")
	streamTimeout, err := time.ParseDuration(GetEnv("STREAM_TIMEOUT", "120s"))
	if err != nil {
		streamTimeout = 120 * time.Second
	}
	return Config{
		OpenAI: OpenAIConfig{
			APIKey:          os.Getenv("OPENAI_API_KEY"),
			APIKeys:         SplitEnv("OPENAI_API_KEYS"),
			BaseURL:         os.Getenv("OPENAI_BASE_URL"),
			Model:           GetEnv("OPENAI_MODEL", "deepseek-v4-flash"),
			FallbackModel:   GetEnv("OPENAI_FALLBACK_MODEL", ""),
			FallbackBaseURL: GetEnv("OPENAI_FALLBACK_BASE_URL", ""),
			StreamTimeout:   streamTimeout,
			RateLimitRPM:    GetEnvInt("RATE_LIMIT_RPM", 0),
			RateLimitTPM:    GetEnvInt("RATE_LIMIT_TPM", 0),
		},
		Server: ServerConfig{
			Port: GetEnv("PORT", "8080"),
		},
		Agent: AgentConfig{
			MaxStep:            GetEnvInt("AGENT_MAX_STEP", 12),
			ContextWarnPct:     GetEnvInt("CONTEXT_WARN_PCT", 40),
			ContextCompressPct: GetEnvInt("CONTEXT_COMPRESS_PCT", 50),
			MaxReconnect:       GetEnvInt("MAX_RECONNECT_ATTEMPTS", 3),
		},
		Raw: RawConfig{
			RawLog:      GetEnv("RAW_LOG", "") == "1",
			RawLogDir:   GetEnv("RAW_LOG_DIR", "logs/raw"),
			UsageDB:     GetEnv("USAGE_DB", "") == "1",
			UsageDBPath: GetEnv("USAGE_DB_PATH", "logs/raw/usage.db"),
		},
		Security: SecurityConfig{
			AllowPrivateURLs: strings.EqualFold(GetEnv("ALLOW_PRIVATE_URLS", "false"), "true"),
		},
	}
}

func LoadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := strings.ReplaceAll(string(data), "\r", "")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
}

func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func GetEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func SplitEnv(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
