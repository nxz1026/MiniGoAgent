package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRunCompress_SharedHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"compressed"}}]}`)
	}))
	defer srv.Close()

	os.Setenv("COMPRESS_API_KEY", "test")
	os.Setenv("COMPRESS_BASE_URL", srv.URL)
	os.Setenv("COMPRESS_MODEL", "gpt-4o-mini")
	defer os.Unsetenv("COMPRESS_API_KEY")
	defer os.Unsetenv("COMPRESS_BASE_URL")
	defer os.Unsetenv("COMPRESS_MODEL")

	result, err := RunCompress(context.Background(), CompressInput{
		Content:     "hello world",
		Instruction: "",
	})
	if err != nil {
		t.Fatalf("RunCompress failed: %v", err)
	}
	if result != "compressed" {
		t.Fatalf("expected compressed, got %s", result)
	}
}
