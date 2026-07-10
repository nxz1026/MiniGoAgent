package protocol

import (
	"net/url"
	"strings"
)

type Vendor int

const (
	VendorUnspecified Vendor = iota
	VendorDeepSeek
	VendorMiniMax
	VendorMiMo
	VendorZhipu
	VendorLongCat
	VendorOllamaCloud
	VendorStepFun
	VendorQwen
)

func matchesVendorHost(baseURL, apex string, canonicals ...string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, c := range canonicals {
		if host == c {
			return true
		}
	}
	return strings.HasSuffix(host, "."+apex)
}

func IsDeepSeek(baseURL string) bool {
	return matchesVendorHost(baseURL, "deepseek.com", "api.deepseek.com")
}

func IsMiniMax(baseURL string) bool {
	return matchesVendorHost(baseURL, "minimaxi.com", "api.minimaxi.com")
}

func IsMiMo(baseURL string) bool {
	return matchesVendorHost(baseURL, "xiaomimimo.com", "api.xiaomimimo.com")
}

func IsZhipu(baseURL string) bool {
	return matchesVendorHost(baseURL, "bigmodel.cn", "open.bigmodel.cn") ||
		matchesVendorHost(baseURL, "z.ai", "api.z.ai")
}

func IsLongCat(baseURL string) bool {
	return matchesVendorHost(baseURL, "longcat.chat", "api.longcat.chat")
}

func IsOllamaCloud(baseURL string) bool {
	return matchesVendorHost(baseURL, "ollama.com", "ollama.com")
}

func IsStepFun(baseURL string) bool {
	return matchesVendorHost(baseURL, "stepfun.com", "api.stepfun.com")
}

func IsQwen(baseURL string) bool {
	return matchesVendorHost(baseURL, "aliyuncs.com", "dashscope.aliyuncs.com")
}

func (v Vendor) String() string {
	switch v {
	case VendorDeepSeek:
		return "deepseek"
	case VendorMiniMax:
		return "minimax"
	case VendorMiMo:
		return "mimo"
	case VendorZhipu:
		return "zhipu"
	case VendorLongCat:
		return "longcat"
	case VendorOllamaCloud:
		return "ollama"
	case VendorStepFun:
		return "stepfun"
	case VendorQwen:
		return "qwen"
	default:
		return "openai"
	}
}

func DetectVendor(baseURL string) Vendor {
	switch {
	case IsDeepSeek(baseURL):
		return VendorDeepSeek
	case IsMiniMax(baseURL):
		return VendorMiniMax
	case IsMiMo(baseURL):
		return VendorMiMo
	case IsZhipu(baseURL):
		return VendorZhipu
	case IsLongCat(baseURL):
		return VendorLongCat
	case IsOllamaCloud(baseURL):
		return VendorOllamaCloud
	case IsStepFun(baseURL):
		return VendorStepFun
	case IsQwen(baseURL):
		return VendorQwen
	default:
		return VendorUnspecified
	}
}
