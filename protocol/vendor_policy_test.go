package protocol

import (
	"encoding/json"
	"testing"
)

func TestResolveReasoningPolicy_ThinkingVendors(t *testing.T) {
	thinkingVendors := []Vendor{VendorDeepSeek, VendorMiniMax, VendorZhipu, VendorLongCat, VendorMiMo, VendorStepFun, VendorQwen}
	for _, v := range thinkingVendors {
		if p := resolveReasoningPolicy(v); p != reasoningThinking {
			t.Errorf("vendor=%v: got policy=%d, want reasoningThinking", v, p)
		}
	}
}

func TestResolveReasoningPolicy_DefaultVendor(t *testing.T) {
	if p := resolveReasoningPolicy(VendorUnspecified); p != reasoningEffort {
		t.Errorf("unspecified vendor: got policy=%d, want reasoningEffort", p)
	}
}

func TestBuildThinkingFields_DeepSeek(t *testing.T) {
	m := map[string]any{}
	buildThinkingFields(m, VendorDeepSeek, reasoningThinking, "", "enabled")
	want := `{"thinking":{"type":"enabled"}}`
	got, _ := json.Marshal(m)
	if string(got) != want {
		t.Errorf("DeepSeek enabled: got %s, want %s", got, want)
	}

	m = map[string]any{}
	buildThinkingFields(m, VendorDeepSeek, reasoningThinking, "", "disabled")
	want = `{"thinking":{"type":"disabled"}}`
	got, _ = json.Marshal(m)
	if string(got) != want {
		t.Errorf("DeepSeek disabled: got %s, want %s", got, want)
	}
}

func TestBuildThinkingFields_MiniMax(t *testing.T) {
	m := map[string]any{}
	buildThinkingFields(m, VendorMiniMax, reasoningThinking, "high", "")
	want := `{"thinking":{"type":"high"}}`
	got, _ := json.Marshal(m)
	if string(got) != want {
		t.Errorf("MiniMax with effort high: got %s, want %s", got, want)
	}

	m = map[string]any{}
	buildThinkingFields(m, VendorMiniMax, reasoningThinking, "", "")
	want = `{"thinking":{"type":"adaptive"}}`
	got, _ = json.Marshal(m)
	if string(got) != want {
		t.Errorf("MiniMax default effort: got %s, want %s", got, want)
	}
}

func TestBuildThinkingFields_Zhipu(t *testing.T) {
	m := map[string]any{}
	buildThinkingFields(m, VendorZhipu, reasoningThinking, "high", "")
	want := `{"thinking":{"type":"high"}}`
	got, _ := json.Marshal(m)
	if string(got) != want {
		t.Errorf("Zhipu with effort high: got %s, want %s", got, want)
	}

	m = map[string]any{}
	buildThinkingFields(m, VendorZhipu, reasoningThinking, "", "")
	want = `{"thinking":{"type":"enabled"}}`
	got, _ = json.Marshal(m)
	if string(got) != want {
		t.Errorf("Zhipu default effort: got %s, want %s", got, want)
	}
}

func TestBuildThinkingFields_LongCat(t *testing.T) {
	m := map[string]any{}
	buildThinkingFields(m, VendorLongCat, reasoningThinking, "", "enabled")
	want := `{"thinking":{"type":"enabled"}}`
	got, _ := json.Marshal(m)
	if string(got) != want {
		t.Errorf("LongCat: got %s, want %s", got, want)
	}
}

func TestBuildThinkingFields_Others(t *testing.T) {
	for _, v := range []Vendor{VendorMiMo, VendorStepFun, VendorQwen} {
		m := map[string]any{}
		buildThinkingFields(m, v, reasoningThinking, "", "high")
		want := `{"thinking":{"type":"high"}}`
		got, _ := json.Marshal(m)
		if string(got) != want {
			t.Errorf("vendor=%v with thinkingType high: got %s, want %s", v, got, want)
		}
	}
}

func TestBuildThinkingFields_EffortPolicy(t *testing.T) {
	m := map[string]any{}
	buildThinkingFields(m, VendorUnspecified, reasoningEffort, "high", "")
	want := `{"reasoning_effort":"high"}`
	got, _ := json.Marshal(m)
	if string(got) != want {
		t.Errorf("effort policy: got %s, want %s", got, want)
	}
}

func TestDefaultHealthEndpoint(t *testing.T) {
	got := defaultHealthEndpoint(VendorUnspecified, "https://api.example.com/v1")
	want := "https://api.example.com/v1/models"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestVendorDefaultEffort(t *testing.T) {
	if e := vendorDefaultEffort(VendorMiniMax); e != "adaptive" {
		t.Errorf("MiniMax default effort: got %s, want adaptive", e)
	}
	if e := vendorDefaultEffort(VendorDeepSeek); e != "enabled" {
		t.Errorf("DeepSeek default effort: got %s, want enabled", e)
	}
}
