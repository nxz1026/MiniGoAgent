package filter

import (
	"fmt"
	"strings"
)

func init() {
	Register("git status", filterGitStatus)
	Register("git log", filterGitLog)
	Register("git diff", filterGitDiff)
	Register("git show", filterGitShow)
	Register("git branch", filterGitBranch)
	Register("git add", filterGitAdd)
	Register("git commit", filterGitCommit)
	Register("git push", filterGitPush)
	Register("git pull", filterGitPull)
	Register("git checkout", filterGitCheckout)
	Register("git stash", filterGitGeneric)
	Register("git fetch", filterGitGeneric)
	Register("git merge", filterGitGeneric)
	Register("git rebase", filterGitGeneric)
}

func filterGitStatus(raw, cmd string) string {
	// Use porcelain if no format specified
	needsPorcelain := !strings.Contains(cmd, "--porcelain") &&
		!strings.Contains(cmd, "--long") &&
		!strings.Contains(cmd, "-v") &&
		!strings.Contains(cmd, "--verbose")

	_ = needsPorcelain
	lines := strings.Split(raw, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "(use \"git") ||
			strings.HasPrefix(trimmed, "(create/copy files") ||
			strings.Contains(trimmed, "(use \"git add") ||
			strings.Contains(trimmed, "(use \"git restore") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 1 && strings.Contains(out[0], "nothing to commit") {
		return "clean\n"
	}
	if len(out) == 0 {
		return "ok\n"
	}
	return strings.Join(out, "\n") + "\n"
}

func filterGitLog(raw, cmd string) string {
	lines := strings.Split(raw, "\n")
	limit := 10
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(out) >= limit {
			out = append(out, fmt.Sprintf("  ... (+%d more commits)", len(lines)-limit))
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, truncateLine(line, 100))
	}
	return strings.Join(out, "\n") + "\n"
}

func filterGitDiff(raw, cmd string) string {
	lines := strings.Split(raw, "\n")
	var out []string
	maxHunk := 50
	var currentFile string
	var hunkShown int
	var hunkSkipped int
	totalAdded := 0
	totalRemoved := 0

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "diff --git") {
			if hunkSkipped > 0 {
				out = append(out, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
				hunkSkipped = 0
			}
			if currentFile != "" {
				out = append(out, fmt.Sprintf("  +%d -%d", totalAdded, totalRemoved))
				totalAdded = 0
				totalRemoved = 0
			}
			parts := strings.Split(line, " b/")
			if len(parts) > 1 {
				currentFile = parts[len(parts)-1]
			} else {
				currentFile = strings.TrimPrefix(line, "diff --git ")
			}
			out = append(out, "")
			out = append(out, currentFile)
			hunkShown = 0
		} else if strings.HasPrefix(line, "@@") {
			if hunkSkipped > 0 {
				out = append(out, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
				hunkSkipped = 0
			}
			hunkShown = 0
			out = append(out, "  "+line)
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			totalAdded++
			if hunkShown < maxHunk {
				out = append(out, "  "+line)
				hunkShown++
			} else {
				hunkSkipped++
			}
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			totalRemoved++
			if hunkShown < maxHunk {
				out = append(out, "  "+line)
				hunkShown++
			} else {
				hunkSkipped++
			}
		} else if hunkShown > 0 && hunkShown < maxHunk && !strings.HasPrefix(line, "\\") {
			out = append(out, "  "+line)
			hunkShown++
		}
	}
	if hunkSkipped > 0 {
		out = append(out, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
	}
	if currentFile != "" {
		out = append(out, fmt.Sprintf("  +%d -%d", totalAdded, totalRemoved))
	}

	if len(out) == 0 {
		return "no changes\n"
	}
	return strings.Join(out, "\n") + "\n"
}

func filterGitShow(raw, cmd string) string {
	return filterGitDiff(raw, cmd)
}

func filterGitBranch(raw, cmd string) string {
	lines := strings.Split(raw, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "remotes/") {
			rest := strings.TrimPrefix(line, "remotes/")
			if idx := strings.Index(rest, "/"); idx >= 0 {
				branch := rest[idx+1:]
				if strings.HasPrefix(branch, "HEAD ") {
					continue
				}
				out = append(out, branch)
			}
		} else {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func filterGitAdd(raw, cmd string) string {
	lines := strings.Split(raw, "\n")
	hasContent := false
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return "ok\n"
	}
	return "ok\n"
}

func filterGitCommit(raw, cmd string) string {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "[") {
			if end := strings.Index(line, "]"); end > 0 {
				content := line[1:end]
				fields := strings.Fields(content)
				if len(fields) >= 2 {
					hash := fields[len(fields)-1]
					if len(hash) >= 7 {
						return fmt.Sprintf("ok %s\n", hash[:7])
					}
				}
			}
		}
	}
	return "ok\n"
}

func filterGitPush(raw, cmd string) string {
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "->") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "->" && i+1 < len(parts) {
					dest := strings.TrimRight(parts[i+1], ":")
					return fmt.Sprintf("ok %s\n", dest)
				}
			}
		}
		if strings.Contains(line, "Everything up-to-date") {
			return "ok (up-to-date)\n"
		}
	}
	return "ok\n"
}

func filterGitPull(raw, cmd string) string {
	if strings.Contains(raw, "Already up to date") || strings.Contains(raw, "Already up-to-date") {
		return "ok (up-to-date)\n"
	}
	// Parse summary: "N files changed, M insertions(+), K deletions(-)"
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "file") && strings.Contains(line, "changed") {
			return strings.TrimSpace(line) + "\n"
		}
	}
	return "ok\n"
}

func filterGitCheckout(raw, cmd string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Switched to a new branch") {
			if idx := strings.LastIndex(line, "'"); idx > 0 {
				return "ok " + line[strings.LastIndex(line[:idx], "'")+1:idx] + " (new)\n"
			}
		}
		if strings.HasPrefix(line, "Switched to branch") {
			if idx := strings.LastIndex(line, "'"); idx > 0 {
				return "ok " + line[strings.LastIndex(line[:idx], "'")+1:idx] + "\n"
			}
		}
		if strings.HasPrefix(line, "Already on") {
			if idx := strings.LastIndex(line, "'"); idx > 0 {
				return "ok " + line[strings.LastIndex(line[:idx], "'")+1:idx] + "\n"
			}
		}
		if strings.HasPrefix(line, "HEAD is now at") {
			parts := strings.Fields(line)
			if len(parts) >= 5 {
				return "ok HEAD " + parts[3] + "\n"
			}
		}
	}
	return "ok\n"
}

func filterGitGeneric(raw, cmd string) string {
	if strings.TrimSpace(raw) == "" {
		return "ok\n"
	}
	return raw
}
