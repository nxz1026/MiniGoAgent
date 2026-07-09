package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRunVision_SharedHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	os.Setenv("VISION_API_KEY", "test")
	os.Setenv("VISION_BASE_URL", srv.URL)
	os.Setenv("VISION_MODEL", "gpt-4o")
	defer os.Unsetenv("VISION_API_KEY")
	defer os.Unsetenv("VISION_BASE_URL")
	defer os.Unsetenv("VISION_MODEL")

	result, err := RunVision(context.Background(), VisionInput{
		ImageURL: "data:image/png;base64," + "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
		Prompt:   "describe",
	})
	if err != nil {
		t.Fatalf("RunVision failed: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %s", result)
	}
}
