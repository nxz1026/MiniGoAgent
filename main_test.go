package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"MiniGoAgent/protocol"
)

func TestExtractLocalImagePath(t *testing.T) {
	tmp, err := os.MkdirTemp("E:\\", "minigoagent-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)
	imgPath := filepath.Join(tmp, "test.png")
	os.WriteFile(imgPath, []byte("fake"), 0644)

	if fp := extractLocalImagePath("describe " + imgPath); fp != imgPath {
		t.Fatalf("expected %s, got %s", imgPath, fp)
	}
	if fp := extractLocalImagePath("not a path"); fp != "" {
		t.Fatalf("expected empty, got %s", fp)
	}
}

type mockProtocol struct{}

func (m *mockProtocol) Chat(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	return &protocol.Response{Content: "mocked"}, nil
}

func (m *mockProtocol) Stream(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
	return nil, nil
}

func TestForwardChat(t *testing.T) {
	m := &chatModel{proto: &mockProtocol{}, model: "test"}
	resp, err := m.forwardChat(context.Background(), protocol.Request{})
	if err != nil {
		t.Fatalf("forwardChat failed: %v", err)
	}
	if resp.Content != "mocked" {
		t.Fatalf("expected mocked content, got %s", resp.Content)
	}
}
