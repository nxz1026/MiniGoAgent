package protocol

import (
	"fmt"
	"strings"
	"sync"
)

type CallRecord struct {
	Model            string
	Vendor           string
	Duration         float64 // seconds
	PromptTokens     int
	CompletionTokens int
}

type Telemetry struct {
	mu       sync.Mutex
	last     CallRecord
	modelCtx map[string]int
}

func NewTelemetry() *Telemetry {
	return &Telemetry{
		modelCtx: map[string]int{
			"gpt-4":             8192,
			"gpt-4-32k":         32768,
			"gpt-4-turbo":       128000,
			"gpt-4o":            128000,
			"gpt-4o-mini":       128000,
			"claude-3-haiku":    200000,
			"claude-3-sonnet":   200000,
			"claude-3-opus":     200000,
			"claude-3-5-haiku":  200000,
			"claude-3-5-sonnet": 200000,
			"deepseek-chat":     65536,
			"deepseek-reasoner": 65536,
		},
	}
}

func (t *Telemetry) Record(model, vendor string, durSec float64, usage *Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = CallRecord{
		Model:            model,
		Vendor:           vendor,
		Duration:         durSec,
		PromptTokens:     0,
		CompletionTokens: 0,
	}
	if usage != nil {
		t.last.PromptTokens = usage.PromptTokens
		t.last.CompletionTokens = usage.CompletionTokens
	}
}

func (t *Telemetry) Last() CallRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

func (t *Telemetry) FormatLine(sep string) string {
	r := t.Last()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%.0fs%s%s", r.Duration, sep, r.Model))
	inTok := humanTok(r.PromptTokens)
	outTok := humanTok(r.CompletionTokens)
	if inTok != "0" || outTok != "0" {
		b.WriteString(fmt.Sprintf("%s↑%s%s↓%s", sep, inTok, sep, outTok))
	}
	maxCtx := ModelContextWindow(r.Model)
	if maxCtx > 0 && r.PromptTokens > 0 {
		pct := float64(r.PromptTokens) * 100 / float64(maxCtx)
		b.WriteString(fmt.Sprintf("%sctx %s/%s %.0f%%", sep, humanTok(r.PromptTokens), humanTok(maxCtx), pct))
	}
	return b.String()
}

func (t *Telemetry) FormatMap() map[string]any {
	r := t.Last()
	maxCtx := ModelContextWindow(r.Model)
	m := map[string]any{
		"duration":    fmt.Sprintf("%.0fs", r.Duration),
		"model":       r.Model,
		"in_tokens":   r.PromptTokens,
		"out_tokens":  r.CompletionTokens,
		"in_tokens_h": humanTok(r.PromptTokens),
		"out_tokens_h": humanTok(r.CompletionTokens),
	}
	if maxCtx > 0 {
		m["ctx_max"] = maxCtx
		m["ctx_max_h"] = humanTok(maxCtx)
		m["ctx_pct"] = fmt.Sprintf("%.0f%%", float64(r.PromptTokens)*100/float64(maxCtx))
	}
	return m
}

func humanTok(n int) string {
	switch {
	case n >= 1000*1000:
		return fmt.Sprintf("%.1fM", float64(n)/(1000*1000))
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func ModelContextWindow(model string) int {
	m := map[string]int{
		"gpt-4":             8192,
		"gpt-4-32k":         32768,
		"gpt-4-turbo":       128000,
		"gpt-4o":            128000,
		"gpt-4o-mini":       128000,
		"claude-3-haiku":    200000,
		"claude-3-sonnet":   200000,
		"claude-3-opus":     200000,
		"claude-3-5-haiku":  200000,
		"claude-3-5-sonnet": 200000,
		"deepseek-chat":     65536,
		"deepseek-reasoner": 65536,
	}
	if v, ok := m[model]; ok {
		return v
	}
	if strings.Contains(model, "128k") || strings.Contains(model, "128K") {
		return 128000
	}
	if strings.Contains(model, "200k") || strings.Contains(model, "200K") {
		return 200000
	}
	return 524288
}
