package tool

import (
	"context"
	"sync"
)

type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
	DecisionCheck
)

type GuardrailResult struct {
	Allowed bool
	Reason  string
}

type GuardrailRule func(ctx context.Context, toolName string, args any) GuardrailResult

type Guardrails struct {
	mu        sync.RWMutex
	allowList map[string]bool
	denyList  map[string]bool
	rules     []GuardrailRule
	registry  *ToolRegistry
}

func NewGuardrails(registry *ToolRegistry) *Guardrails {
	return &Guardrails{
		allowList: make(map[string]bool),
		denyList:  make(map[string]bool),
		registry:  registry,
	}
}

func (g *Guardrails) Allow(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.allowList[name] = true
	delete(g.denyList, name)
}

func (g *Guardrails) Deny(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denyList[name] = true
	delete(g.allowList, name)
}

func (g *Guardrails) AllowAll(names ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range names {
		g.allowList[n] = true
		delete(g.denyList, n)
	}
}

func (g *Guardrails) DenyAll(names ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range names {
		g.denyList[n] = true
		delete(g.allowList, n)
	}
}

func (g *Guardrails) AddRule(rule GuardrailRule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rules = append(g.rules, rule)
}

func (g *Guardrails) Check(ctx context.Context, toolName string, args any) GuardrailResult {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.denyList[toolName] {
		return GuardrailResult{Allowed: false, Reason: "tool is denied"}
	}

	if len(g.allowList) > 0 && !g.allowList[toolName] {
		return GuardrailResult{Allowed: false, Reason: "tool not in allow list"}
	}

	for _, rule := range g.rules {
		result := rule(ctx, toolName, args)
		if !result.Allowed {
			return result
		}
	}

	if g.registry != nil {
		if t := g.registry.Get(toolName); t != nil && !g.registry.Check(ctx, toolName) {
			return GuardrailResult{Allowed: false, Reason: "tool is unavailable"}
		}
	}

	return GuardrailResult{Allowed: true}
}

func (g *Guardrails) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.allowList = make(map[string]bool)
	g.denyList = make(map[string]bool)
	g.rules = nil
}
