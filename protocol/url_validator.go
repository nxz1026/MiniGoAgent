package protocol

import (
	"net"
	"net/url"
	"os"
	"strings"
)

func ValidateBaseURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return &URLValidationError{URL: rawURL, Reason: "parse error: " + err.Error()}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &URLValidationError{URL: rawURL, Reason: "unsupported scheme: " + u.Scheme}
	}
	host := u.Hostname()
	if host == "" {
		return &URLValidationError{URL: rawURL, Reason: "missing host"}
	}
	if os.Getenv("ALLOW_PRIVATE_URLS") != "true" {
		if isPrivateHost(host) {
			return &URLValidationError{URL: rawURL, Reason: "private/internal URL blocked (set ALLOW_PRIVATE_URLS=true to override)"}
		}
	}
	return nil
}

func IsPrivateHost(host string) bool {
	return isPrivateHost(host)
}

func isPrivateHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			ip = net.ParseIP(host[1 : len(host)-1])
		}
	}
	if ip != nil {
		return isPrivateIP(ip)
	}
	lower := strings.ToLower(host)
	privateHosts := []string{
		"localhost", "127.0.0.1", "0.0.0.0",
		"::1", "0.0.0.0", "169.254.0.1",
	}
	for _, h := range privateHosts {
		if lower == h {
			return true
		}
	}
	if strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	if strings.HasPrefix(lower, "10.") || strings.HasPrefix(lower, "192.168.") {
		return true
	}
	if strings.HasPrefix(lower, "172.") {
		parts := strings.SplitN(lower, ".", 4)
		if len(parts) >= 2 {
			second := 0
			for _, c := range parts[1] {
				if c < '0' || c > '9' {
					break
				}
				second = second*10 + int(c-'0')
			}
			if second >= 16 && second <= 31 {
				return true
			}
		}
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	privateRanges := []struct {
		network string
	}{
		{"127.0.0.0/8"},
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"169.254.0.0/16"},
		{"::1/128"},
		{"fc00::/7"},
		{"fe80::/10"},
	}
	for _, r := range privateRanges {
		_, cidr, err := net.ParseCIDR(r.network)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func ValidateProxyEnv() error {
	proxyVars := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy",
		"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
	}
	for _, key := range proxyVars {
		val := os.Getenv(key)
		if val == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if !strings.HasPrefix(val, "http://") && !strings.HasPrefix(val, "https://") && !strings.HasPrefix(val, "socks5://") && !strings.HasPrefix(val, "socks5h://") {
			return &URLValidationError{URL: val, Reason: "invalid proxy scheme in " + key + " (expected http/https/socks5)"}
		}
	}
	return nil
}

type URLValidationError struct {
	URL    string
	Reason string
}

func (e *URLValidationError) Error() string {
	if e.URL != "" && e.Reason != "" {
		return "invalid URL " + e.URL + ": " + e.Reason
	}
	if e.Reason != "" {
		return e.Reason
	}
	return "invalid URL: " + e.URL
}
