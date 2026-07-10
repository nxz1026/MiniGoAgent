package protocol

import (
	"context"
	"strings"
	"testing"
)

func TestValidateBaseURL_RejectsPrivate(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_URLS", "false")
	privateURLs := []string{
		"http://localhost/v1",
		"http://127.0.0.1/v1",
		"http://0.0.0.0/v1",
		"http://[::1]/v1",
		"http://10.0.0.1/v1",
		"http://192.168.1.1/v1",
		"http://172.16.0.1/v1",
	}
	for _, u := range privateURLs {
		if err := ValidateBaseURL(u); err == nil {
			t.Errorf("expected %q to be rejected", u)
		}
	}
}

func TestValidateBaseURL_AllowsPublic(t *testing.T) {
	publicURLs := []string{
		"https://api.openai.com/v1",
		"https://api.deepseek.com/v1",
		"https://open.bigmodel.cn/v1",
		"https://api.minimax.chat/v1",
	}
	for _, u := range publicURLs {
		if err := ValidateBaseURL(u); err != nil {
			t.Errorf("expected %q to be allowed, got: %v", u, err)
		}
	}
}

func TestValidateBaseURL_RejectsInvalidScheme(t *testing.T) {
	invalidURLs := []string{
		"ftp://example.com/v1",
		"file:///etc/passwd",
		"javascript:alert(1)",
	}
	for _, u := range invalidURLs {
		if err := ValidateBaseURL(u); err == nil {
			t.Errorf("expected %q to be rejected for invalid scheme", u)
		}
	}
}

func TestValidateBaseURL_EmptyAllowed(t *testing.T) {
	if err := ValidateBaseURL(""); err != nil {
		t.Errorf("empty URL should be allowed, got: %v", err)
	}
}

func TestRedactString_MasksAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "sk key",
			input:  "key is sk-live-abcdefghijklmnopqrstuvwxyz123456",
			expect: "****************************************",
		},
		{
			name:   "no secret",
			input:  "hello world",
			expect: "hello world",
		},
		{
			name:   "empty string",
			input:  "",
			expect: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactString(tt.input)
			if tt.name == "sk key" {
				if strings.Contains(result, "sk-live-abcdefghijklmnopqrstuvwxyz123456") {
					t.Errorf("secret not masked: %s", result)
				}
			} else if result != tt.expect {
				t.Errorf("got %q, want %q", result, tt.expect)
			}
		})
	}
}

func TestRedactString_HandlesUTF8(t *testing.T) {
	input := "hello 世界 sk-live-abcdefghijklmnopqrstuvwxyz123456 中文"
	result := RedactString(input)
	if strings.Contains(result, "sk-live-abcdefghijklmnopqrstuvwxyz123456") {
		t.Errorf("secret not masked in UTF-8 string: %s", result)
	}
	if !strings.Contains(result, "世界") || !strings.Contains(result, "中文") {
		t.Errorf("UTF-8 content corrupted: %s", result)
	}
}

func TestRateLimiter_Basic(t *testing.T) {
	rl := NewRateLimiter(60, 0)
	if rl == nil {
		t.Fatal("NewRateLimiter returned nil")
	}
	ctx := context.Background()
	if err := rl.Wait(ctx, 10); err != nil {
		t.Errorf("first request should pass: %v", err)
	}
}
