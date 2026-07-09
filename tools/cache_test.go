package tools

import (
	"testing"
)

func TestIsCacheable(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"ls", true},
		{"dir", true},
		{"git status", true},
		{"git log --oneline -5", true},
		{"git add .", false},
		{"git commit -m 'test'", false},
		{"mkdir foo", false},
		{"del file.txt", false},
		{"echo hello", true},
		{"unknown_cmd", false},
		{"  ls  ", true},
	}
	for _, tc := range tests {
		got := isCacheable(tc.cmd)
		if got != tc.want {
			t.Errorf("isCacheable(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}
