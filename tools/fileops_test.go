package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// write
	r, err := WriteFile(context.Background(), WriteFileInput{Path: path, Content: "line1\nline2\nline3\n"})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if r == "" {
		t.Fatal("empty result")
	}

	// read full
	content, err := ReadFile(context.Background(), ReadFileInput{Path: path})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if content == "" {
		t.Fatal("empty content")
	}

	// read with offset and limit
	content, err = ReadFile(context.Background(), ReadFileInput{Path: path, Offset: 2, Limit: 1})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
}

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world foo"), 0644)

	r, err := EditFile(context.Background(), EditFileInput{Path: path, OldText: "foo", NewText: "bar"})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if r == "" {
		t.Fatal("empty result")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello world bar" {
		t.Fatalf("expected 'hello world bar', got %s", string(data))
	}

	// no match
	_, err = EditFile(context.Background(), EditFileInput{Path: path, OldText: "nonexistent", NewText: "x"})
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

func TestGlobFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b"), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("text"), 0644)

	r, err := GlobFiles(context.Background(), GlobInput{Pattern: "*.go", Root: dir})
	if err != nil {
		t.Fatalf("GlobFiles: %v", err)
	}
	if r == "" {
		t.Fatal("empty result")
	}

	r, err = GlobFiles(context.Background(), GlobInput{Pattern: "*.xyz", Root: dir})
	if err != nil {
		t.Fatalf("GlobFiles: %v", err)
	}
}
