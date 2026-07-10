package protocol

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	compressCache   = map[string]compressCacheEntry{}
	compressCacheMu sync.RWMutex
)

type compressCacheEntry struct {
	result    string
	timestamp time.Time
}

const compressCacheTTL = 30 * time.Second
const compressCacheMax = 500

func CachedCompressContent(content string, ct ContentType) string {
	if len(content) < 256 {
		return content
	}
	h := sha256.Sum256([]byte(content))
	key := fmt.Sprintf("%x-%d", h[:8], ct)

	compressCacheMu.RLock()
	entry, ok := compressCache[key]
	compressCacheMu.RUnlock()
	if ok && time.Since(entry.timestamp) < compressCacheTTL {
		return entry.result
	}

	result := CompressContent(content, ct)

	compressCacheMu.Lock()
	if len(compressCache) >= compressCacheMax {
		for k := range compressCache {
			delete(compressCache, k)
			break
		}
	}
	compressCache[key] = compressCacheEntry{result: result, timestamp: time.Now()}
	compressCacheMu.Unlock()
	return result
}

var (
	reTimestamp  = regexp.MustCompile(`^\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}(:\d{2})?(\.\d+)?(Z|[+-]\d{2}:?\d{2})?\s+`)
	reHunkHeader = regexp.MustCompile(`^@@ -\d+,\d+ +\+\d+,\d+ @@`)
	reBlanks     = regexp.MustCompile(`\n{3,}`)
	reTrailingWS = regexp.MustCompile(`[ \t]+\n`)
)

const maxHunks = 50
const maxSearchResults = 10

func CompressContent(content string, ct ContentType) string {
	if len(content) < 256 {
		return content
	}
	switch ct {
	case ContentJsonArray:
		return compressJSONArray(content)
	case ContentBuildOutput:
		return compressBuildOutput(content)
	case ContentSearchResults:
		return compressSearchResults(content)
	case ContentGitDiff:
		return compressGitDiff(content)
	default:
		return content
	}
}

func compressJSONArray(content string) string {
	var b strings.Builder
	b.Grow(len(content))
	inStr := false
	esc := false
	for i := 0; i < len(content); i++ {
		c := content[i]
		if inStr {
			b.WriteByte(c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			b.WriteByte(c)
		case ' ', '\t', '\n', '\r':
			continue
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func compressBuildOutput(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content))
	seenBlank := false
	lineCount := 0
	for _, line := range lines {
		stripped := reTimestamp.ReplaceAllString(line, "")
		stripped = reANSIEscape.ReplaceAllString(stripped, "")
		stripped = strings.TrimRight(stripped, " \t\r")
		if stripped == "" {
			if !seenBlank && lineCount > 0 {
				b.WriteByte('\n')
				seenBlank = true
			}
			continue
		}
		seenBlank = false
		lineCount++
		b.WriteString(stripped)
		b.WriteByte('\n')
	}
	if lineCount > 0 && b.Len() > 0 {
		return strings.TrimRight(b.String(), "\n")
	}
	return b.String()
}

func compressSearchResults(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content) / 2)
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "---") || strings.HasPrefix(strings.TrimSpace(line), "===") {
			continue
		}
		if reSearch.MatchString(line) {
			if count >= maxSearchResults {
				continue
			}
			count++
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		if reURL.MatchString(line) {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		if count > 0 && b.Len() > 0 {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func compressGitDiff(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content))
	hunkCount := 0
	for _, line := range lines {
		if reHunkHeader.MatchString(line) {
			hunkCount++
			if hunkCount > maxHunks {
				if hunkCount == maxHunks+1 {
					b.WriteString("... (+" + itoa(len(lines)-maxHunks) + " more hunks truncated)\n")
				}
				continue
			}
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
