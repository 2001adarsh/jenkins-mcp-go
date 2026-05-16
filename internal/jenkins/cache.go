package jenkins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultMaxCacheBytes is the soft cap applied by Cache.Fetch when no override
// is configured. 1 GiB strikes a balance between holding enough finished-build
// logs to be useful and not surprising operators on smaller machines.
const DefaultMaxCacheBytes int64 = 1 * 1024 * 1024 * 1024

// finishedMarker matches the last line Jenkins emits when a build is done.
// We only persist a log to disk after seeing this marker, so a cache hit can
// never serve a partial / mid-flight log.
var finishedMarker = regexp.MustCompile(`(?m)^Finished: (SUCCESS|FAILURE|ABORTED|UNSTABLE|NOT_BUILT)\s*$`)

var unsafeSlugChar = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// ConsoleCache serves console logs from disk when the build has finished, and
// transparently falls back to the network when it has not.
//
// The cache is LRU by mtime, capped at MaxBytes. Writes are serialized.
type ConsoleCache struct {
	Client   *Client
	Dir      string
	MaxBytes int64

	mu sync.Mutex
}

// NewConsoleCache constructs a cache rooted at dir. The directory is created
// if missing.
func NewConsoleCache(c *Client, dir string) (*ConsoleCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", dir, err)
	}
	return &ConsoleCache{
		Client:   c,
		Dir:      dir,
		MaxBytes: DefaultMaxCacheBytes,
	}, nil
}

// Path returns the deterministic on-disk path for a (jobPath, buildNumber) log.
// The slug is sanitized to a flat filename so directory traversal cannot escape
// the cache root.
func (cc *ConsoleCache) Path(jobPath string, buildNumber int64) string {
	slug := strings.ReplaceAll(strings.Trim(jobPath, "/"), "/", "__")
	slug = unsafeSlugChar.ReplaceAllString(slug, "_")
	if len(slug) > 180 {
		slug = slug[:180]
	}
	return filepath.Join(cc.Dir, fmt.Sprintf("%s-%d.log", slug, buildNumber))
}

// Fetch returns the full consoleText body for a build and, if cached on disk,
// the cache path.
//
// Behavior:
//   - buildNumber == 0 (lastBuild) is never cached — its identity is unstable.
//   - For positive build numbers, a cache hit short-circuits the network.
//   - A network response is written to disk only if the body contains the
//     `Finished: <result>` marker, guaranteeing the cached file is complete.
func (cc *ConsoleCache) Fetch(ctx context.Context, jobPath string, buildNumber int64) (body []byte, cachePath string, err error) {
	if buildNumber > 0 {
		cachePath = cc.Path(jobPath, buildNumber)
		if b, e := os.ReadFile(cachePath); e == nil {
			now := time.Now()
			_ = os.Chtimes(cachePath, now, now)
			return b, cachePath, nil
		}
		cachePath = ""
	}

	url := JobAPIPath(jobPath) + "/" + BuildRef(buildNumber) + "/consoleText"
	body, err = cc.Client.Get(ctx, url, nil)
	if err != nil {
		return nil, "", err
	}

	if buildNumber > 0 && finishedMarker.Match(tailBytes(body, 256)) {
		path := cc.Path(jobPath, buildNumber)
		cc.mu.Lock()
		defer cc.mu.Unlock()
		if writeErr := os.WriteFile(path, body, 0o644); writeErr == nil {
			cachePath = path
			cc.evictIfOverCap()
		}
	}
	return body, cachePath, nil
}

// evictIfOverCap deletes oldest-mtime files until total cache size <= MaxBytes.
// Caller must hold cc.mu.
func (cc *ConsoleCache) evictIfOverCap() {
	entries, err := os.ReadDir(cc.Dir)
	if err != nil {
		return
	}
	type item struct {
		path  string
		size  int64
		mtime time.Time
	}
	items := make([]item, 0, len(entries))
	var total int64
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join(cc.Dir, de.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= cc.MaxBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })
	for _, it := range items {
		if total <= cc.MaxBytes {
			return
		}
		if err := os.Remove(it.path); err == nil {
			total -= it.size
		}
	}
}

func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}
