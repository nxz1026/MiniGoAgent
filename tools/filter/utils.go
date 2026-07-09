package filter

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

func truncateLine(line string, width int) string {
	if utf8.RuneCountInString(line) <= width {
		return line
	}
	runes := []rune(line)
	return string(runes[:width-3]) + "..."
}

func humanSize(bytes uint64) string {
	if bytes >= 1<<20 {
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(1<<20))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%.1fK", float64(bytes)/1024.0)
	}
	return fmt.Sprintf("%dB", bytes)
}

func dedupLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	var result []string
	prev := lines[0]
	count := 1
	for _, line := range lines[1:] {
		if line == prev {
			count++
			continue
		}
		if count > 1 {
			result = append(result, fmt.Sprintf("  (previous line repeated %d times)", count))
		} else {
			result = append(result, prev)
		}
		prev = line
		count = 1
	}
	if count > 1 {
		result = append(result, fmt.Sprintf("  (previous line repeated %d times)", count))
	} else {
		result = append(result, prev)
	}
	return result
}

func emptyLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

type kvEntry struct {
	key   string
	value int
}

func sortByValueDesc(m map[string]int) []kvEntry {
	var entries []kvEntry
	for k, v := range m {
		entries = append(entries, kvEntry{k, v})
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].value > entries[i].value {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	return entries
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}


