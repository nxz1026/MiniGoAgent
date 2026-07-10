package protocol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type RawLogProcessor struct {
	dir     string
	file    *os.File
	date    string
	mu      sync.Mutex
	enabled bool
}

func NewRawLogProcessor(dir string) *RawLogProcessor {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &RawLogProcessor{enabled: false}
	}
	return &RawLogProcessor{dir: dir, enabled: true}
}

func (p *RawLogProcessor) Process(ctx context.Context, chunk Chunk) error {
	if !p.enabled {
		return nil
	}
	switch chunk.Type {
	case ChunkRawRequest, ChunkRawResponse, ChunkRawError:
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.file == nil || p.date != time.Now().Format("2006-01-02") {
			p.rotate()
		}
		line, _ := json.Marshal(map[string]any{
			"ts":   time.Now().Format(time.RFC3339),
			"kind": chunk.Type.String(),
			"data": chunk.Text,
		})
		if _, err := p.file.Write(append(line, '\n')); err != nil {
			p.enabled = false
			return err
		}
	}
	return nil
}

func (p *RawLogProcessor) Name() string {
	return "raw"
}

func (p *RawLogProcessor) rotate() {
	if p.file != nil {
		p.file.Close()
	}
	date := time.Now().Format("2006-01-02")
	path := filepath.Join(p.dir, date+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		p.enabled = false
		return
	}
	p.file = f
	p.date = date
}

func (p *RawLogProcessor) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file != nil {
		p.file.Close()
		p.file = nil
		p.date = ""
	}
}

func init() {
	RegisterChunkType(ChunkRawRequest, "raw_request")
	RegisterChunkType(ChunkRawResponse, "raw_response")
	RegisterChunkType(ChunkRawError, "raw_error")
}
