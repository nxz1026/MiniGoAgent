package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

type ToolSchema struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Message struct {
	Role             Role
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	ToolCallID       string
	Name             string
	MultiContent     []map[string]any
}

type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolSchema
	Temperature *float64
	TopP        *float64
	MaxTokens   *int
	Stop        []string
}

type Response struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	Usage            *Usage
	Warning          string
}

type ChunkType int

const (
	ChunkText ChunkType = iota
	ChunkReasoning
	ChunkToolCallStart
	ChunkToolCall
	ChunkUsage
	ChunkWarn
	ChunkDone
	ChunkError
	ChunkRawRequest
	ChunkRawResponse
	ChunkRawError
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Chunk struct {
	Type     ChunkType
	Text     string
	ToolCall *ToolCall
	Usage    *Usage
	Warning  string
	Error    error
}

type Protocol interface {
	Chat(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

type Config struct {
	APIKey             string
	APIKeys            []string
	BaseURL            string
	Model              string
	StreamTimeout      time.Duration
	RateLimitRPM       int
	RateLimitTPM       int
	ContextWarnPct     int
	ContextCompressPct int
	MaxReconnect       int
	HealthCheckURL     string
	FallbackModel      string
	FallbackBaseURL    string
}

type contextKey string

const (
	CtxLogf        contextKey = "protocol_logf"
	CtxRetryNotify contextKey = "protocol_retry_notify"
	CtxSessionID   contextKey = "protocol_session_id"
)

type Factory func(Config) (Protocol, error)

const MaxRetries = 10
const maxBackoff = 15 * time.Second

type AuthError struct {
	Vendor Vendor
	Status int
	HasKey bool
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("auth error (vendor=%d, status=%d, has_key=%v)", e.Vendor, e.Status, e.HasKey)
}

type APIError struct {
	Vendor Vendor
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("api error (vendor=%d, status=%d)", e.Vendor, e.Status)
	}
	return fmt.Sprintf("api error (vendor=%d, status=%d): %s", e.Vendor, e.Status, e.Body)
}

type StreamInterruptedError struct {
	Emitted bool
	Err     error
}

func (e *StreamInterruptedError) Error() string {
	return fmt.Sprintf("stream interrupted (emitted=%v): %v", e.Emitted, e.Err)
}

func (e *StreamInterruptedError) Unwrap() error {
	return e.Err
}

type CircuitBreakerState int

const (
	Closed CircuitBreakerState = iota
	Open
	HalfOpen
)

type CircuitBreakerOptions struct {
	Timeout           time.Duration
	HalfOpenMaxCalls  int
	CheckHTTPCodes    []int
	CheckErrors       []error
	FailureThreshold  int // default 5
}

func NewCircuitBreaker(opts CircuitBreakerOptions) *CircuitBreaker {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 5
	}
	return &CircuitBreaker{
		failureThreshold: opts.FailureThreshold,
		options:          opts,
		state:            Closed,
	}
}

type CircuitBreaker struct {
	failureThreshold int
	lastFailure      time.Time
	state            CircuitBreakerState
	options          CircuitBreakerOptions
	mutex            sync.Mutex
}

func (cb *CircuitBreaker) isFailure(err error) bool {
	if err == nil {
		return false
	}
	for _, e := range cb.options.CheckErrors {
		if errors.Is(err, e) {
			return true
		}
	}
	if len(cb.options.CheckHTTPCodes) > 0 {
		var ae *AuthError
		if errors.As(err, &ae) {
			for _, code := range cb.options.CheckHTTPCodes {
				if ae.Status == code {
					return true
				}
			}
		}
	}
	return true
}

func (cb *CircuitBreaker) Check(err error) bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.state == Closed {
		if err != nil && cb.isFailure(err) {
			cb.failureThreshold--
			if cb.failureThreshold <= 0 {
				cb.state = Open
				cb.lastFailure = time.Now()
				return false
			}
		}
		return true
	}

	if cb.state == Open {
		if time.Since(cb.lastFailure) >= cb.options.Timeout {
			cb.state = HalfOpen
			return true
		}
		return false
	}

	if cb.state == HalfOpen {
		if err != nil && cb.isFailure(err) {
			cb.state = Open
			cb.lastFailure = time.Now()
			return false
		}
		return true
	}

	return true
}

func (cb *CircuitBreaker) Success() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.state == HalfOpen {
		cb.failureThreshold = cb.options.FailureThreshold
		cb.state = Closed
	}
	if cb.state == Closed {
		cb.failureThreshold = cb.options.FailureThreshold
	}
}

var (
	registry   = map[string]Factory{}
	registryMu sync.RWMutex
)

func Register(kind string, f Factory) {
	registryMu.Lock()
	registry[kind] = f
	registryMu.Unlock()
}

func New(kind string, cfg Config) (Protocol, error) {
	registryMu.RLock()
	f, ok := registry[kind]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider kind: %s", kind)
	}
	return f(cfg)
}

var (
	chunkTypeNames = map[ChunkType]string{}
	chunkTypeMu    sync.RWMutex
)

func RegisterChunkType(t ChunkType, name string) {
	chunkTypeMu.Lock()
	chunkTypeNames[t] = name
	chunkTypeMu.Unlock()
}

func (t ChunkType) String() string {
	chunkTypeMu.RLock()
	name, ok := chunkTypeNames[t]
	chunkTypeMu.RUnlock()
	if ok {
		return name
	}
	return fmt.Sprintf("ChunkType(%d)", int(t))
}
