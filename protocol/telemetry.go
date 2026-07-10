package protocol

import (
	"context"
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
	mu         sync.Mutex
	last       CallRecord
	modelCtx   map[string]int
	warnPct    int
	comprPct   int
	lastWarn   bool
	lastCompr  bool
	chunkBytes int64
	toolCalls  int64
	errors     int64
	sessionID  string
	tracker    *UsageTracker
}

func NewTelemetry() *Telemetry {
	return &Telemetry{
		warnPct:  40,
		comprPct: 50,
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

func (t *Telemetry) SetTracker(ut *UsageTracker) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tracker = ut
}

func (t *Telemetry) SetSessionID(sid string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = sid
}

func (t *Telemetry) Record(model, vendor string, durSec float64, usage *Usage) (warn, compress bool) {
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	return t.record(sid, model, vendor, durSec, usage)
}

func (t *Telemetry) record(sessionID, model, vendor string, durSec float64, usage *Usage) (warn, compress bool) {
	t.mu.Lock()
	promptTks, completionTks := 0, 0
	if usage != nil {
		promptTks = usage.PromptTokens
		completionTks = usage.CompletionTokens
	}
	t.last = CallRecord{
		Model:            model,
		Vendor:           vendor,
		Duration:         durSec,
		PromptTokens:     promptTks,
		CompletionTokens: completionTks,
	}
	t.lastWarn, t.lastCompr = false, false
	if usage != nil && t.warnPct > 0 {
		maxCtx := ModelContextWindow(model)
		if maxCtx > 0 {
			pct := usage.PromptTokens * 100 / maxCtx
			if t.comprPct > 0 && pct >= t.comprPct {
				t.lastCompr = true
				compress = true
			}
			if pct >= t.warnPct {
				t.lastWarn = true
				warn = true
			}
		}
	}
	tracker := t.tracker
	t.mu.Unlock()
	if tracker != nil {
		_ = tracker.Record(sessionID, model, vendor, durSec, promptTks, completionTks)
	}
	return
}

func (t *Telemetry) RecordFromContext(ctx context.Context, model, vendor string, durSec float64, usage *Usage) (warn, compress bool) {
	sid := ""
	if sid, ok := ctx.Value(CtxSessionID).(string); ok && sid != "" {
		return t.record(sid, model, vendor, durSec, usage)
	}
	return t.record(sid, model, vendor, durSec, usage)
}

func (t *Telemetry) SetThresholds(warnPct, compressPct int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.warnPct = warnPct
	t.comprPct = compressPct
}

func (t *Telemetry) LastWarning() (warn, compress bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastWarn, t.lastCompr
}

func (t *Telemetry) RecordChunkBytes(n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.chunkBytes += n
}

func (t *Telemetry) RecordToolCall() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.toolCalls++
}

func (t *Telemetry) RecordError() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errors++
}

func (t *Telemetry) RecordUsage(usage *Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if usage == nil {
		return
	}
	t.last.PromptTokens = usage.PromptTokens
	t.last.CompletionTokens = usage.CompletionTokens
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
		"duration":     fmt.Sprintf("%.0fs", r.Duration),
		"model":        r.Model,
		"in_tokens":    r.PromptTokens,
		"out_tokens":   r.CompletionTokens,
		"in_tokens_h":  humanTok(r.PromptTokens),
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
		"step-2-16k":        16384,
		"step-1-128k":       131072,
		"qwen-turbo":        8192,
		"qwen-plus":         32768,
		"qwq-32b-preview":   32768,
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
	if strings.Contains(model, "step") || strings.Contains(model, "Step") {
		return 128000
	}
	if strings.Contains(model, "qwen") || strings.Contains(model, "Qwen") {
		return 32768
	}
	return 524288
}
