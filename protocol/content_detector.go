package protocol

import (
	"regexp"
	"strings"
)

type ContentType int

const (
	ContentPlainText ContentType = iota
	ContentJsonArray
	ContentBuildOutput
	ContentSearchResults
	ContentGitDiff
	ContentSourceCode
)

var (
	reBuildLog   = regexp.MustCompile(`(?m)^\d{4}[-/]\d{2}[-/]\d{2}\s+\d{2}:\d{2}`)
	reSearch     = regexp.MustCompile(`(?m)^\d+\.\s+.+`)
	reGitDiff    = regexp.MustCompile(`(?m)^(diff --git|@@ |--- |\+\+\+ )`)
	reLogLevel   = regexp.MustCompile(`(?im)^(DEBUG|INFO|WARN|ERROR|FATAL|TRACE)\b`)
	reURL        = regexp.MustCompile(`(?m)^\s+URL:\s*https?://`)
	reANSIEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

var defaultPredictor = NewContentPredictor(true)

func DetectContentType(content string) ContentType {
	return defaultPredictor.Predict(content)
}

func detectContentTypeHeuristic(content string) ContentType {
	if strings.HasPrefix(strings.TrimSpace(content), "[") ||
		strings.HasPrefix(strings.TrimSpace(content), "{") {
		if !strings.Contains(content, "\n") {
			return ContentJsonArray
		}
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	matched := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if matched > 5 {
			break
		}
		switch {
		case reGitDiff.MatchString(line):
			matched++
		case reBuildLog.MatchString(line) || reLogLevel.MatchString(line):
			matched++
		case reSearch.MatchString(line) && isSearchResultLine(line):
			matched++
		case lineNeedsFurtherCheck(line):
			matched++
		}
	}
	if matched > 3 {
		return detectFromMatched(lines)
	}
	return detectFromContent(content)
}

func isSearchResultLine(line string) bool {
	return len(line) > 30 && strings.Contains(line, "http")
}

func lineNeedsFurtherCheck(line string) bool {
	return len(line) > 20 && strings.ContainsAny(line, "=@()[]{}+-<>")
}

func detectFromMatched(lines []string) ContentType {
	diffCount, logCount, searchCount := 0, 0, 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case reGitDiff.MatchString(line):
			diffCount++
		case reBuildLog.MatchString(line) || reLogLevel.MatchString(line):
			logCount++
		case reSearch.MatchString(line) || reURL.MatchString(line):
			searchCount++
		}
	}
	switch {
	case diffCount > logCount && diffCount > searchCount:
		return ContentGitDiff
	case logCount > diffCount && logCount > searchCount:
		return ContentBuildOutput
	case searchCount > diffCount && searchCount > logCount:
		return ContentSearchResults
	}
	return ContentPlainText
}

func detectFromContent(content string) ContentType {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "diff --git") ||
		strings.HasPrefix(trimmed, "--- ") ||
		strings.HasPrefix(trimmed, "Index: ") {
		return ContentGitDiff
	}
	if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed, "\n{") {
		return ContentJsonArray
	}
	first := strings.SplitN(trimmed, "\n", 2)[0]
	if reBuildLog.MatchString(first) || reLogLevel.MatchString(first) {
		return ContentBuildOutput
	}
	if strings.Count(content, "\n") > 20 && hasCodePatterns(content) {
		return ContentSourceCode
	}
	return ContentPlainText
}

func hasCodePatterns(content string) bool {
	patterns := []string{"func ", "class ", "import ", "package ", "#include", "def ", "fn ",
		"=>", "->", ";;", "if (", "for (", "while (", "```"}
	count := 0
	for _, p := range patterns {
		if strings.Contains(content, p) {
			count++
		}
	}
	return count >= 2
}
