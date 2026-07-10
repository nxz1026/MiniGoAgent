package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

type PredictionModel interface {
	Predict(content string) ContentType
	Update(content string, actual ContentType)
}

type ContentPredictor struct {
	cache   sync.Map // key: sha256(content[:4096]) -> ContentType
	enabled bool
	model   PredictionModel
}

func NewContentPredictor(enabled bool) *ContentPredictor {
	return &ContentPredictor{
		enabled: enabled,
		model:   &heuristicModel{},
	}
}

func (p *ContentPredictor) Predict(content string) ContentType {
	if !p.enabled {
		return detectContentTypeHeuristic(content)
	}
	key := contentHash(content)
	if v, ok := p.cache.Load(key); ok {
		return v.(ContentType)
	}
	ct := p.model.Predict(content)
	p.cache.Store(key, ct)
	return ct
}

func (p *ContentPredictor) Invalidate(content string) {
	key := contentHash(content)
	p.cache.Delete(key)
}

func contentHash(content string) string {
	sample := content
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	h := sha256.Sum256([]byte(sample))
	return hex.EncodeToString(h[:])
}

type heuristicModel struct{}

func (h *heuristicModel) Predict(content string) ContentType {
	return detectContentTypeHeuristic(content)
}

func (h *heuristicModel) Update(content string, actual ContentType) {
	// no-op for heuristic model; reserved for ML-backed model
}
