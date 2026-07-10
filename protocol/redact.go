package protocol

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(sk[-_](?:live|test|prod)?[-_][a-zA-Z0-9]{20,})`),
		regexp.MustCompile(`(?i)(sk-ant-api[-_][a-zA-Z0-9]{20,})`),
		regexp.MustCompile(`(?i)(AIza[0-9A-Za-z\-_]{35})`),
		regexp.MustCompile(`(?i)(ghp_[a-zA-Z0-9]{36})`),
		regexp.MustCompile(`(?i)(github_pat_[a-zA-Z0-9_]{60,})`),
		regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),
		regexp.MustCompile(`(?i)(eyJ[a-zA-Z0-9_-]{10,}\.eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,})`),
		regexp.MustCompile(`(?i)(Bearer\s+[a-zA-Z0-9\-._~+/]+=*)`),
		regexp.MustCompile(`(?i)(Basic\s+[a-zA-Z0-9\-._~+/]+=*)`),
		regexp.MustCompile(`(?i)(xox[baprs]-[a-zA-Z0-9-]+)`),
	}
	sensitiveJSONKeys = []string{
		"api_key", "apikey", "api_secret", "apisecret",
		"token", "access_token", "refresh_token",
		"password", "passwd", "secret", "client_secret",
		"authorization", "x-api-key", "x-auth-token",
	}
)

func RedactString(s string) string {
	if s == "" {
		return s
	}
	result := s
	for _, pat := range secretPatterns {
		result = pat.ReplaceAllStringFunc(result, func(m string) string {
			if len(m) <= 8 {
				return strings.Repeat("*", len(m))
			}
			return m[:4] + strings.Repeat("*", len(m)-8) + m[len(m)-4:]
		})
	}
	return result
}

func RedactHeaders(h map[string][]string) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		if isSensitiveHeader(k) {
			out[k] = []string{redactValue(v)}
		} else {
			out[k] = v
		}
	}
	return out
}

func isSensitiveHeader(key string) bool {
	lower := strings.ToLower(key)
	sensitive := []string{"authorization", "x-api-key", "x-auth-token", "proxy-authorization", "cookie"}
	for _, s := range sensitive {
		if lower == s {
			return true
		}
	}
	return false
}

func redactValue(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	v := vals[0]
	if len(v) <= 8 {
		return strings.Repeat("*", len(v))
	}
	return v[:4] + strings.Repeat("*", len(v)-8) + v[len(v)-4:]
}

func RedactBody(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	if !utf8.Valid(b) {
		return []byte(RedactString(string(b)))
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return []byte(RedactString(string(b)))
	}
	redactMap(obj)
	out, _ := json.Marshal(obj)
	return out
}

func redactMap(m map[string]any) {
	for k, v := range m {
		lower := strings.ToLower(k)
		if isSensitiveKey(lower) {
			m[k] = "[REDACTED]"
			continue
		}
		switch val := v.(type) {
		case map[string]any:
			redactMap(val)
		case []any:
			redactSlice(val)
		case string:
			m[k] = RedactString(val)
		}
	}
}

func redactSlice(arr []any) {
	for i, v := range arr {
		switch val := v.(type) {
		case map[string]any:
			redactMap(val)
			arr[i] = val
		case []any:
			redactSlice(val)
		case string:
			arr[i] = RedactString(val)
		}
	}
}

func isSensitiveKey(key string) bool {
	for _, sk := range sensitiveJSONKeys {
		if key == sk {
			return true
		}
	}
	return false
}

func RedactURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return RedactString(rawURL)
	}
	if u.User != nil {
		if pwd, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "[REDACTED]")
			_ = pwd
		}
	}
	q := u.Query()
	for _, key := range q {
		for i := range key {
			if len(key[i]) > 8 {
				key[i] = key[i][:4] + "****" + key[i][len(key[i])-4:]
			} else {
				key[i] = "****"
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
