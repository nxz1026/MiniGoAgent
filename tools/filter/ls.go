package filter

import (
	"fmt"
	"regexp"
	"strings"
)

// noiseDirs are directories collapsed in ls output.
var noiseDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true,
	"__pycache__": true, ".next": true, "dist": true, "build": true,
	".cache": true, ".turbo": true, ".vercel": true, ".pytest_cache": true,
	".venv": true, "venv": true, "env": true, "coverage": true,
	".nyc_output": true, ".DS_Store": true, "Thumbs.db": true,
	".idea": true, ".vscode": true, ".vs": true,
}

// lsDateRE matches the date-time anchor in ls -la output.
var lsDateRE = regexp.MustCompile(
	`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(?:\d{4}|\d{2}:\d{2})\s+`,
)

func init() {
	Register("ls", filterLS)
	Register("dir", filterLS)
}

func filterLS(raw, cmd string) string {
	showAll := strings.Contains(cmd, " -a") || strings.Contains(cmd, " --all") || strings.Contains(cmd, "/a")
	entries, summary := compactLS(raw, showAll)
	if entries == "" {
		return raw
	}
	return entries + summary
}

type lsEntry struct {
	isDir bool
	name  string
	size  string
}

func compactLS(raw string, showAll bool) (entries, summary string) {
	var dirs, files []lsEntry
	extCount := map[string]int{}
	parsedCount := 0

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "total ") || strings.HasPrefix(line, "  ") {
			continue
		}
		if isDotDir(line) {
			continue
		}
		ft, name, size, ok := parseLSLine(line)
		if !ok {
			continue
		}
		parsedCount++

		if !showAll && noiseDirs[name] {
			continue
		}

		if ft == 'd' {
			dirs = append(dirs, lsEntry{isDir: true, name: name})
		} else {
			if idx := strings.LastIndex(name, "."); idx >= 0 {
				extCount[name[idx:]]++
			} else {
				extCount["(no ext)"]++
			}
			files = append(files, lsEntry{name: name, size: size})
		}
	}

	if len(dirs) == 0 && len(files) == 0 {
		if parsedCount == 0 {
			return "", ""
		}
		return "(empty)\n", ""
	}

	var b strings.Builder
	for _, d := range dirs {
		b.WriteString(d.name)
		b.WriteString("/\n")
	}
	for _, f := range files {
		b.WriteString(f.name)
		b.WriteString("  ")
		b.WriteString(f.size)
		b.WriteString("\n")
	}

	extSummary := ""
	if len(extCount) > 0 {
		entries := sortByValueDesc(extCount)
		n := minInt(5, len(entries))
		var parts []string
		for _, e := range entries[:n] {
			parts = append(parts, fmt.Sprintf("%d %s", e.value, e.key))
		}
		extSummary = " (" + strings.Join(parts, ", ") + ")"
		if len(entries) > n {
			extSummary += fmt.Sprintf(", +%d more", len(entries)-n)
		}
		extSummary += ")"
	}

	summary = fmt.Sprintf("\nSummary: %d files, %d dirs%s\n",
		len(files), len(dirs), extSummary)

	return b.String(), summary
}

// parseLSLine extracts file type, name, and size from an ls -la line.
// Uses the date field as an anchor to handle variable-width owner/group columns.
func parseLSLine(line string) (fileType byte, name, size string, ok bool) {
	loc := lsDateRE.FindStringIndex(line)
	if loc == nil {
		return
	}
	name = strings.TrimSpace(line[loc[1]:])
	before := strings.Fields(line[:loc[0]])
	if len(before) < 4 {
		return
	}
	perms := before[0]
	if len(perms) == 0 {
		return
	}
	fileType = perms[0]

	// size is the last parseable number before the date
	var rawSize uint64
	for i := len(before) - 1; i >= 0; i-- {
		if s, err := fmt.Sscanf(before[i], "%d", &rawSize); err == nil && s == 1 {
			break
		}
	}

	ok = true
	return fileType, name, humanSize(rawSize), true
}

func isDotDir(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "." || trimmed == ".." || strings.HasSuffix(trimmed, " .") || strings.HasSuffix(trimmed, " ..")
}
