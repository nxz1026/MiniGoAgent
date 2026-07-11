package protocol

import "strings"

func isThinkingVendor(v Vendor) bool {
	switch v {
	case VendorDeepSeek, VendorZhipu, VendorMiniMax, VendorLongCat, VendorMiMo, VendorStepFun, VendorQwen:
		return true
	}
	return false
}

func isEffortVendor(v Vendor) bool {
	return !isThinkingVendor(v)
}

func vendorDefaultEffort(v Vendor) string {
	switch v {
	case VendorMiniMax:
		return "adaptive"
	default:
		return "enabled"
	}
}

func vendorDefaultThinkingType(v Vendor) string {
	switch v {
	case VendorDeepSeek:
		return "enabled"
	default:
		return "enabled"
	}
}

func buildThinkingFields(m map[string]any, v Vendor, policy reasoningPolicy, effort, thinkingType string) {
	switch policy {
	case reasoningThinking:
		switch v {
		case VendorDeepSeek:
			if thinkingType == "disabled" {
				m["thinking"] = map[string]string{"type": "disabled"}
				return
			}
			// 支持测试中指定的 thinkingType
			t := thinkingType
			if t == "" {
				t = vendorDefaultThinkingType(v)
			}
			m["thinking"] = map[string]string{"type": t}

		case VendorMiniMax:
			t := effort
			if t == "" {
				t = vendorDefaultEffort(v)
			}
			m["thinking"] = map[string]string{"type": t}

		case VendorZhipu:
			t := effort
			if t == "" {
				t = vendorDefaultEffort(v)
			}
			m["thinking"] = map[string]string{"type": t}

		case VendorLongCat, VendorMiMo, VendorStepFun, VendorQwen:
			t := thinkingType
			if t == "" {
				t = vendorDefaultThinkingType(v)
			}
			m["thinking"] = map[string]string{"type": t}

		default:
			t := thinkingType
			if t == "" {
				t = vendorDefaultThinkingType(v)
			}
			m["thinking"] = map[string]string{"type": t}
		}

	case reasoningEffort:
		if effort != "" {
			m["reasoning_effort"] = effort
		}
	}
}

func resolveReasoningPolicy(v Vendor) reasoningPolicy {
	if isThinkingVendor(v) {
		return reasoningThinking
	}
	return reasoningEffort
}

func defaultHealthEndpoint(_ Vendor, baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/models"
}
