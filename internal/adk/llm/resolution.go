package llm

import (
	"fmt"

	"MiniGoAgent/internal/config"
	"MiniGoAgent/protocol"
)

type ModelRef struct {
	Model    string
	BaseURL  string
	Provider string
}

func Resolve(ref ModelRef, cfg *config.Config) (*Bridge, error) {
	apiKey := cfg.OpenAI.APIKey
	baseURL := ref.BaseURL
	if baseURL == "" {
		baseURL = cfg.OpenAI.BaseURL
	}
	model := ref.Model
	if model == "" {
		model = cfg.OpenAI.Model
	}

	if apiKey == "" || baseURL == "" {
		return nil, fmt.Errorf("API key and base URL are required")
	}
	if err := protocol.ValidateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("base URL validation failed: %w", err)
	}

	proto, err := protocol.New("openai", protocol.Config{
		APIKey:             apiKey,
		APIKeys:            cfg.OpenAI.APIKeys,
		BaseURL:            baseURL,
		Model:              model,
		StreamTimeout:      cfg.OpenAI.StreamTimeout,
		RateLimitRPM:       cfg.OpenAI.RateLimitRPM,
		RateLimitTPM:       cfg.OpenAI.RateLimitTPM,
		ContextWarnPct:     cfg.Agent.ContextWarnPct,
		ContextCompressPct: cfg.Agent.ContextCompressPct,
		MaxReconnect:       cfg.Agent.MaxReconnect,
		FallbackModel:      cfg.OpenAI.FallbackModel,
		FallbackBaseURL:    cfg.OpenAI.FallbackBaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create protocol failed: %w", err)
	}

	return NewBridge(proto, model), nil
}

func NewBridgeFromConfig(cfg *config.Config) (*Bridge, error) {
	return Resolve(ModelRef{
		Model:   cfg.OpenAI.Model,
		BaseURL: cfg.OpenAI.BaseURL,
	}, cfg)
}
