package filter

import (
	"strings"
)

func noopFilter(raw, cmd string) string {
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = stripANSI(strings.TrimRight(lines[i], "\r"))
	}
	lines = dedupLines(emptyLines(lines))
	return strings.Join(lines, "\n") + "\n"
}
