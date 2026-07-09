package tools

import (
	"os"
	"strings"
	"sync"
	"time"

	"MiniGoAgent/tools/log"
)

type cacheEntry struct {
	result    string
	timestamp time.Time
}

var (
	cmdCache = map[string]*cacheEntry{}
	cacheMu  sync.Mutex
	cacheTTL = 30 * time.Second
)

// commands whose results are safe to cache (read-only)
var cacheablePrefixes = []string{
	"ls", "dir", "git status", "git log", "git diff", "git branch",
	"git stash list", "git tag", "where", "which", "type", "echo",
	"more", "help", "ver", "systeminfo", "tasklist",
}

var writePrefixes = []string{
	"git add", "git commit", "git push", "git pull", "git checkout",
	"git merge", "git rebase", "git reset", "git rm", "git clean",
	"git stash push", "git stash drop", "git tag -d", "git branch -d",
	"git branch -D", "mkdir", "rmdir", "del", "copy", "move",
	"ren", "attrib", "icacls", "takeown",
}

func init() {
	if t := os.Getenv("CACHE_TTL"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			cacheTTL = d
		}
	}
}

func lookupCache(cmd string) (string, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	entry, ok := cmdCache[cmd]
	if !ok {
		return "", false
	}
	if time.Since(entry.timestamp) > cacheTTL {
		delete(cmdCache, cmd)
		return "", false
	}
	log.Debug("cache hit: %s", cmd)
	return entry.result, true
}

func storeCache(cmd, result string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cmdCache) > 200 {
		for k := range cmdCache {
			delete(cmdCache, k)
			break
		}
	}
	cmdCache[cmd] = &cacheEntry{result: result, timestamp: time.Now()}
}

func isCacheable(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range writePrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	for _, p := range cacheablePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}
