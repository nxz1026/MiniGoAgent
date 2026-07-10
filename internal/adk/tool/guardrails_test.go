package tool

import (
	"context"
	"testing"
)

func TestGuardrails_AllowByDefault(t *testing.T) {
	g := NewGuardrails(nil)
	result := g.Check(context.Background(), "any_tool", nil)
	if !result.Allowed {
		t.Error("should be allowed by default")
	}
}

func TestGuardrails_Deny(t *testing.T) {
	g := NewGuardrails(nil)
	g.Deny("bad_tool")

	result := g.Check(context.Background(), "bad_tool", nil)
	if result.Allowed {
		t.Error("should be denied")
	}
	if result.Reason != "tool is denied" {
		t.Errorf("want 'tool is denied', got %q", result.Reason)
	}
}

func TestGuardrails_Allow(t *testing.T) {
	g := NewGuardrails(nil)
	g.Allow("safe_tool")

	result := g.Check(context.Background(), "safe_tool", nil)
	if !result.Allowed {
		t.Error("should be allowed")
	}
}

func TestGuardrails_AllowOverridesDeny(t *testing.T) {
	g := NewGuardrails(nil)
	g.Deny("tool_a")
	g.Allow("tool_a")

	result := g.Check(context.Background(), "tool_a", nil)
	if !result.Allowed {
		t.Error("allow should override deny")
	}
}

func TestGuardrails_DenyOverridesAllow(t *testing.T) {
	g := NewGuardrails(nil)
	g.Allow("tool_a")
	g.Deny("tool_a")

	result := g.Check(context.Background(), "tool_a", nil)
	if result.Allowed {
		t.Error("deny should override allow when set last")
	}
}

func TestGuardrails_AllowAll(t *testing.T) {
	g := NewGuardrails(nil)
	g.AllowAll("tool_a", "tool_b")

	if !g.Check(context.Background(), "tool_a", nil).Allowed {
		t.Error("tool_a should be allowed")
	}
	if !g.Check(context.Background(), "tool_b", nil).Allowed {
		t.Error("tool_b should be allowed")
	}
	if g.Check(context.Background(), "tool_c", nil).Allowed {
		t.Error("tool_c should be denied (not in allow list)")
	}
}

func TestGuardrails_DenyAll(t *testing.T) {
	g := NewGuardrails(nil)
	g.DenyAll("tool_a", "tool_b")

	if g.Check(context.Background(), "tool_a", nil).Allowed {
		t.Error("tool_a should be denied")
	}
	if g.Check(context.Background(), "tool_b", nil).Allowed {
		t.Error("tool_b should be denied")
	}
}

func TestGuardrails_CustomRule(t *testing.T) {
	g := NewGuardrails(nil)

	g.AddRule(func(ctx context.Context, toolName string, args any) GuardrailResult {
		if toolName == "read_file" && args != nil {
			return GuardrailResult{Allowed: false, Reason: "read_file requires explicit args approval"}
		}
		return GuardrailResult{Allowed: true}
	})

	if !g.Check(context.Background(), "read_file", nil).Allowed {
		t.Error("read_file without args should be allowed")
	}

	result := g.Check(context.Background(), "read_file", "some args")
	if result.Allowed {
		t.Error("read_file with args should be denied by custom rule")
	}
}

func TestGuardrails_MultipleRules(t *testing.T) {
	g := NewGuardrails(nil)

	g.AddRule(func(ctx context.Context, name string, args any) GuardrailResult {
		if name == "dangerous" {
			return GuardrailResult{Allowed: false, Reason: "dangerous tool"}
		}
		return GuardrailResult{Allowed: true}
	})

	g.AddRule(func(ctx context.Context, name string, args any) GuardrailResult {
		if name == "nuclear" {
			return GuardrailResult{Allowed: false, Reason: "nuclear tool"}
		}
		return GuardrailResult{Allowed: true}
	})

	if g.Check(context.Background(), "dangerous", nil).Allowed {
		t.Error("dangerous should be denied")
	}
	if g.Check(context.Background(), "nuclear", nil).Allowed {
		t.Error("nuclear should be denied")
	}
	if !g.Check(context.Background(), "safe", nil).Allowed {
		t.Error("safe should be allowed")
	}
}

func TestGuardrails_CheckFnIntegration(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	tool.WithCheck(func(ctx context.Context) bool { return false })
	r.Register("add", tool)

	g := NewGuardrails(r)

	result := g.Check(context.Background(), "add", nil)
	if result.Allowed {
		t.Error("should be denied when check_fn returns false")
	}
	if result.Reason != "tool is unavailable" {
		t.Errorf("want 'tool is unavailable', got %q", result.Reason)
	}
}

func TestGuardrails_CheckFnSkipIfDenyList(t *testing.T) {
	r := NewToolRegistry()

	g := NewGuardrails(r)
	g.Deny("add")

	result := g.Check(context.Background(), "add", nil)
	if result.Allowed {
		t.Error("should be denied by deny list")
	}
}

func TestGuardrails_Clear(t *testing.T) {
	g := NewGuardrails(nil)
	g.Deny("tool_a")
	g.AddRule(func(ctx context.Context, name string, args any) GuardrailResult {
		return GuardrailResult{Allowed: false}
	})
	g.Clear()

	result := g.Check(context.Background(), "tool_a", nil)
	if !result.Allowed {
		t.Error("should be allowed after clear")
	}
}

func TestGuardrails_EmptyRegistry(t *testing.T) {
	r := NewToolRegistry()
	g := NewGuardrails(r)

	result := g.Check(context.Background(), "unknown", nil)
	if !result.Allowed {
		t.Error("should allow unknown tool with empty registry")
	}
}
