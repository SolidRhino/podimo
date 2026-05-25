---
date: 2026-05-24T22:22:28+0200
author: SolidRhino
commit: 8c05dd7
branch: go-rewrite
repository: podimo
topic: "Comprehensive Go rewrite hardening"
tags: [design, security, performance, go, hardening]
status: ready
parent: .rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md
last_updated: 2026-05-24T22:22:28+0200
last_updated_by: SolidRhino
---

# Design: Comprehensive Go Rewrite Hardening

## Summary

This design implements all 10 improvement areas identified in the research artifact as a cohesive hardening pass. A new generic `BoundedMap` utility provides bounded, TTL-aware caching to eliminate memory leaks in `App.clients`, `RateLimiter.ips`, and `FileCache.keyLocks`. Server timeouts, credential redaction, strict config validation, proper auth error mapping (`AuthenticationError` → 401), brittle string-matching removal, GraphQL response size limits, and RSS context cancellation checks complete the hardening.

## Requirements

From the research artifact and developer decisions:

1. `http.Server` must have `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`, and `MaxHeaderBytes` (`main.go:149`).
2. `loggingMiddleware` must redact the password segment from `/feed/{username}/{password}/...` URLs before logging (`main.go:174`).
3. `App.clients`, `RateLimiter.ips`, and `FileCache.keyLocks` must not grow unbounded (`main.go:41`, `main.go:47`, `podimo/cache.go:14`).
4. `handleFeed` and `handleFeedPath` must return HTTP 401 for `*podimo.AuthenticationError` instead of 500 (`main.go:318`).
5. `GetPodcasts` must not use `strings.Contains(err.Error(), "Unauthorized")` for error classification (`podimo/client.go:305`).
6. `parseBool` and `parseDuration` must return errors on invalid input, causing startup failure (`config.go:127`).
7. `GraphQLClient.Query` must cap response body reads to prevent OOM (`podimo/graphql.go:67`).
8. `PodcastsToRss` must check `ctx.Err()` between chunks and at goroutine start (`podimo/rss.go:72`).
9. `URLHeadInfo` must check `ctx.Err()` during retry sleep (`podimo/rss.go:232`).
10. The error taxonomy must support `errors.As` for idiomatic Go error inspection.

## Current State Analysis

The Go rewrite resolved Python-era issues (async/sync bridge, serial pagination, image validation) but introduced Go-specific gaps: unbounded in-memory maps, missing server timeouts, and concurrency edge cases. The codebase uses custom error structs (`PodcastNotFoundError`, `AuthenticationError`) but only direct type assertions (`err.(*podimo.PodcastNotFoundError)`), which breaks if errors are ever wrapped with `fmt.Errorf("...: %w", err)`. Configuration parsing silently ignores invalid env values. DEBUG logging leaks path-embedded credentials. GraphQL responses are read unbounded into memory. RSS goroutines churn through all episodes even after request cancellation.

### Key Discoveries

- `main.go:149-154` — `http.Server` has zero timeouts; vulnerable to slowloris and resource exhaustion.
- `main.go:174-180` — `loggingMiddleware` logs `r.URL.String()` verbatim, including `/feed/alice/secret123/...` credentials.
- `main.go:41-42` — `App.clients map[string]*http.Client` grows indefinitely with no eviction.
- `main.go:47` — `RateLimiter.ips map[string][]time.Time` accumulates one key per IP forever; old IPs are never purged.
- `main.go:318-374` — `handleFeed` checks `*PodcastNotFoundError` → 404, but `*AuthenticationError` falls through to 500.
- `main.go:488-506` — `checkAuth()` returns `AuthenticationError` on invalid credentials, but handlers swallow the specific error and return generic 401.
- `config.go:127` — `parseBool` silently returns `false` for any non-matching string (e.g., `"TRUE "`).
- `config.go:136` — `parseDuration` silently falls back to default for non-integer strings (e.g., `"1h"`).
- `podimo/client.go:305` — `GetPodcasts` uses `strings.Contains(err.Error(), "Unauthorized")` to classify auth failures; a single wording change from Podimo would misclassify auth as `PodcastNotFoundError`.
- `podimo/graphql.go:67` — `io.ReadAll(res.Body)` reads entire response with no size cap.
- `podimo/rss.go:72-89` — Chunk model spawns goroutines per episode with no global limit; no `ctx.Err()` check between chunks.
- `podimo/rss.go:232-245` — `URLHeadInfo` retries with hard-coded `time.Sleep(1 * time.Second)` and no cancellation check during sleep.
- `podimo/cache.go:14` — `FileCache.keyLocks map[string]*sync.Mutex` grows one mutex per unique cache key forever; file expiry removes JSON but not the mutex.

## Scope

### Building

1. **BoundedMap foundation** — Generic TTL/LRU bounded map (`podimo/boundedmap.go` + tests).
2. **Config strictness** — `parseBool`/`parseDuration` return errors; `LoadConfig` fails on invalid values.
3. **Server hardening** — Timeouts on `http.Server`.
4. **Error taxonomy improvements** — Remove brittle string matching; add `errors.As` support; map `AuthenticationError` to 401.
5. **Logging security** — Redact password segment from feed-path URLs in DEBUG logs.
6. **Memory leak fixes** — Replace unbounded maps with `BoundedMap` in `App.clients`, `RateLimiter.ips`, and `FileCache.keyLocks`.
7. **GraphQL transport** — Response body size limit via `io.LimitReader`.
8. **RSS concurrency** — Context cancellation checks in `PodcastsToRss` and `URLHeadInfo`.
9. **Tests** — Cover all modified behaviors and the new `BoundedMap` type.

### Not Building

- Typed GraphQL response structs (large refactor, no immediate security/correctness benefit).
- Generic pagination abstraction for `GetPodcasts` (out of scope for hardening).
- Circuit breaker or backoff for GraphQL network errors (deferred; current 30s client timeout is sufficient for now).
- `X-Forwarded-For` support in `RateLimiter` (deployment should handle rate limiting at proxy layer).
- Global goroutine semaphore across feed requests (context checks are sufficient).
- `PodimoClient.Login` auth state restructuring (mutable fields are load-bearing per research; changing Podimo's API contract is out of scope).
- Redis or external cache replacement for `FileCache` (deferred per AGENTS.md).

## Decisions

### Decision 1: Shared bounded utility for memory leaks

**Explored:**
- Option A: Shared `BoundedMap` generic type — one abstraction, consistent eviction semantics, tested once.
- Option B: Ad-hoc local fixes — simpler per-file but inconsistent behavior across `App.clients`, `RateLimiter.ips`, and `FileCache.keyLocks`.

**Decision:** Use a shared `BoundedMap[K comparable, V any]` utility in `podimo/`. It provides TTL-based expiration, max-size LRU eviction, background cleanup, and thread-safe operations. All three unbounded maps (`App.clients`, `RateLimiter.ips`, `FileCache.keyLocks`) migrate to it. For `keyLocks`, we use a generous max size to minimize LRU eviction of active mutexes.

**Evidence:** Precedent from research — "Every caching/concurrency fix must include eviction logic." The pattern-finder found `FileCache`'s per-key mutex is the best existing pattern; `BoundedMap` extends it with boundedness.

### Decision 2: RateLimiter uses RemoteAddr only

**Explored:**
- Option A: Check `X-Forwarded-For` with a `TRUST_PROXY` toggle — needed for proxy deployments but introduces IP spoofing risk.
- Option B: Keep `RemoteAddr` only — simpler, spoof-proof. Proxy-layer rate limiting (nginx `limit_req`) handles deployments behind reverse proxies.

**Decision:** Keep `r.RemoteAddr` only. Document that proxy-layer rate limiting is recommended for reverse proxy deployments.

### Decision 3: RSS concurrency uses context checks only

**Explored:**
- Option A: Context cancellation checks between chunks and at goroutine start — minimal change, no new dependencies.
- Option B: Context checks + global semaphore across all feed requests — prevents thundering herd but adds external dependency (`golang.org/x/sync/semaphore`) and cross-request coupling.

**Decision:** Context checks only. The chunk-local `WaitGroup` already limits concurrent goroutines to 5 per feed request, which is sufficient for a single self-hosted instance.

### Decision 4: Invalid env values cause startup failure

**Inherited from research Developer Context.** `parseBool` and `parseDuration` must return errors. `LoadConfig` propagates them, causing `main()` to log and `os.Exit(1)`.

### Decision 5: DEBUG logs redact password in path

**Inherited from research Developer Context.** Keep logging feed URLs but strip the password segment (replace with `REDACTED`).

## Architecture

### podimo/boundedmap.go — NEW

```go
package podimo

import (
	"container/list"
	"sync"
	"time"
)

type BoundedMapOptions struct {
	MaxSize         int
	TTL             time.Duration
	CleanupInterval time.Duration
}

type entry[V any] struct {
	value     V
	expiresAt time.Time
	elem      *list.Element
}

type BoundedMap[K comparable, V any] struct {
	opts  BoundedMapOptions
	mu    sync.RWMutex
	items map[K]*entry[V]
	lru   *list.List
	stop  chan struct{}
}

func NewBoundedMap[K comparable, V any](opts BoundedMapOptions) *BoundedMap[K, V] {
	bm := &BoundedMap[K, V]{
		opts:  opts,
		items: make(map[K]*entry[V]),
		lru:   list.New(),
		stop:  make(chan struct{}),
	}
	if opts.CleanupInterval > 0 {
		go bm.cleanupLoop()
	}
	return bm
}

func (bm *BoundedMap[K, V]) Get(key K) (V, bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	return bm.get(key)
}

func (bm *BoundedMap[K, V]) get(key K) (V, bool) {
	e, ok := bm.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if bm.opts.TTL > 0 && time.Now().After(e.expiresAt) {
		bm.removeEntry(key)
		var zero V
		return zero, false
	}
	bm.lru.MoveToFront(e.elem)
	return e.value, true
}

func (bm *BoundedMap[K, V]) Set(key K, value V, ttl time.Duration) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.set(key, value, ttl)
}

func (bm *BoundedMap[K, V]) set(key K, value V, ttl time.Duration) {
	if e, ok := bm.items[key]; ok {
		e.value = value
		if ttl > 0 {
			e.expiresAt = time.Now().Add(ttl)
		}
		bm.lru.MoveToFront(e.elem)
		return
	}
	elem := bm.lru.PushFront(key)
	e := &entry[V]{value: value, elem: elem}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	bm.items[key] = e
	if bm.opts.MaxSize > 0 && bm.lru.Len() > bm.opts.MaxSize {
		bm.evictLRU()
	}
}

func (bm *BoundedMap[K, V]) GetOrSet(key K, factory func() V, ttl time.Duration) V {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if v, ok := bm.get(key); ok {
		return v
	}
	value := factory()
	bm.set(key, value, ttl)
	return value
}

func (bm *BoundedMap[K, V]) Delete(key K) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.removeEntry(key)
}

func (bm *BoundedMap[K, V]) removeEntry(key K) {
	if e, ok := bm.items[key]; ok {
		bm.lru.Remove(e.elem)
		delete(bm.items, key)
	}
}

func (bm *BoundedMap[K, V]) evictLRU() {
	back := bm.lru.Back()
	if back == nil {
		return
	}
	key := back.Value.(K)
	bm.removeEntry(key)
}

func (bm *BoundedMap[K, V]) Len() int {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return len(bm.items)
}

func (bm *BoundedMap[K, V]) Stop() {
	close(bm.stop)
}

func (bm *BoundedMap[K, V]) cleanupLoop() {
	ticker := time.NewTicker(bm.opts.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			bm.cleanup()
		case <-bm.stop:
			return
		}
	}
}

func (bm *BoundedMap[K, V]) cleanup() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.opts.TTL <= 0 {
		return
	}
	now := time.Now()
	for key, e := range bm.items {
		if now.After(e.expiresAt) {
			bm.removeEntry(key)
		}
	}
}
```

### podimo/boundedmap_test.go — NEW

```go
package podimo

import (
	"sync"
	"testing"
	"time"
)

func TestBoundedMap_GetSet(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	bm.Set("a", 1, time.Hour)
	v, ok := bm.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected cache hit with value 1, got %v, ok=%v", v, ok)
	}
}

func TestBoundedMap_MissingKey(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	_, ok := bm.Get("missing")
	if ok {
		t.Fatalf("expected miss")
	}
}

func TestBoundedMap_TTLExpiration(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{
		MaxSize:         10,
		CleanupInterval: 50 * time.Millisecond,
	})
	bm.Set("a", 1, 10*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	_, ok := bm.Get("a")
	if ok {
		t.Fatalf("expected expiration after TTL")
	}
	bm.Stop()
}

func TestBoundedMap_MaxSizeEviction(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 2})
	bm.Set("a", 1, time.Hour)
	bm.Set("b", 2, time.Hour)
	bm.Set("c", 3, time.Hour)
	if _, ok := bm.Get("a"); ok {
		t.Fatalf("expected a to be evicted (LRU)")
	}
	if _, ok := bm.Get("b"); !ok {
		t.Fatalf("expected b to exist")
	}
	if _, ok := bm.Get("c"); !ok {
		t.Fatalf("expected c to exist")
	}
}

func TestBoundedMap_LRUUpdateOnGet(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 2})
	bm.Set("a", 1, time.Hour)
	bm.Set("b", 2, time.Hour)
	bm.Get("a")               // touch a, moves to front
	bm.Set("c", 3, time.Hour) // should evict b, not a
	if _, ok := bm.Get("a"); !ok {
		t.Fatalf("expected a to exist (recently touched)")
	}
	if _, ok := bm.Get("b"); ok {
		t.Fatalf("expected b to be evicted (LRU)")
	}
}

func TestBoundedMap_ConcurrentAccess(t *testing.T) {
	bm := NewBoundedMap[int, int](BoundedMapOptions{MaxSize: 100})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bm.Set(n, n, time.Hour)
			if v, ok := bm.Get(n); !ok || v != n {
				t.Errorf("expected %d, got %v, ok=%v", n, v, ok)
			}
		}(i)
	}
	wg.Wait()
}

func TestBoundedMap_GetOrSet(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	v := bm.GetOrSet("a", func() int { return 42 }, time.Hour)
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	v2 := bm.GetOrSet("a", func() int { return 99 }, time.Hour)
	if v2 != 42 {
		t.Fatalf("expected cached 42, got %d", v2)
	}
}

func TestBoundedMap_Delete(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	bm.Set("a", 1, time.Hour)
	bm.Delete("a")
	if _, ok := bm.Get("a"); ok {
		t.Fatalf("expected delete to remove key")
	}
}

func TestBoundedMap_Len(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	if bm.Len() != 0 {
		t.Fatalf("expected len 0, got %d", bm.Len())
	}
	bm.Set("a", 1, time.Hour)
	if bm.Len() != 1 {
		t.Fatalf("expected len 1, got %d", bm.Len())
	}
}
```

### config.go:47-148 — MODIFY

```go
func LoadConfig() (*Config, error) {
	_ = godotenv.Load(".env")

	debug, err := parseBool(os.Getenv("DEBUG"))
	if err != nil {
		return nil, fmt.Errorf("DEBUG: %w", err)
	}
	localCreds, err := parseBool(os.Getenv("LOCAL_CREDENTIALS"))
	if err != nil {
		return nil, fmt.Errorf("LOCAL_CREDENTIALS: %w", err)
	}
	storeTokens, err := parseBool(getEnv("STORE_TOKENS_ON_DISK", "true"))
	if err != nil {
		return nil, fmt.Errorf("STORE_TOKENS_ON_DISK: %w", err)
	}
	tokenCacheTime, err := parseDuration(os.Getenv("TOKEN_CACHE_TIME"), 5*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("TOKEN_CACHE_TIME: %w", err)
	}
	podcastCacheTime, err := parseDuration(os.Getenv("PODCAST_CACHE_TIME"), 6*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("PODCAST_CACHE_TIME: %w", err)
	}
	headCacheTime, err := parseDuration(os.Getenv("HEAD_CACHE_TIME"), 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("HEAD_CACHE_TIME: %w", err)
	}
	publicFeeds, err := parseBool(os.Getenv("PUBLIC_FEEDS"))
	if err != nil {
		return nil, fmt.Errorf("PUBLIC_FEEDS: %w", err)
	}
	videoEnabled, err := parseBool(os.Getenv("ENABLE_VIDEO"))
	if err != nil {
		return nil, fmt.Errorf("ENABLE_VIDEO: %w", err)
	}
	videoCheckEnabled, err := parseBool(os.Getenv("ENABLE_VIDEO_CHECK"))
	if err != nil {
		return nil, fmt.Errorf("ENABLE_VIDEO_CHECK: %w", err)
	}

	cfg := &Config{
		Hostname:          getEnv("PODIMO_HOSTNAME", "localhost:12104"),
		BindHost:          getEnv("PODIMO_BIND_HOST", "127.0.0.1:12104"),
		Protocol:          getEnv("PODIMO_PROTOCOL", "http"),
		HTTPProxy:         os.Getenv("HTTP_PROXY"),
		ZenRowsAPI:        os.Getenv("ZENROWS_API"),
		ScraperAPI:        os.Getenv("SCRAPER_API"),
		CacheDir:          getEnv("CACHE_DIR", "./cache"),
		BlockListFile:     getEnv("BLOCK_LIST_FILE", "./.block-list"),
		Debug:             debug,
		LocalCredentials:  localCreds,
		Email:             os.Getenv("PODIMO_EMAIL"),
		Password:          os.Getenv("PODIMO_PASSWORD"),
		GraphQLURL:        "https://podimo.com/graphql",
		StoreTokensOnDisk: storeTokens,
		TokenCacheTime:    tokenCacheTime,
		PodcastCacheTime:  podcastCacheTime,
		HeadCacheTime:     headCacheTime,
		PublicFeeds:       publicFeeds,
		VideoEnabled:      videoEnabled,
		VideoCheckEnabled: videoCheckEnabled,
		VideoTitleSuffix:  os.Getenv("VIDEO_TITLE_SUFFIX"),
		Locales: []string{
			"nl-NL", "de-DE", "da-DK", "es-ES", "en-US",
			"es-MX", "no-NO", "fi-FI", "en-GB",
		},
		Regions: []Region{
			{Code: "nl", Name: "Nederland"},
			{Code: "de", Name: "Deutschland"},
			{Code: "dk", Name: "Danmark"},
			{Code: "es", Name: "España"},
			{Code: "latam", Name: "America latina"},
			{Code: "en", Name: "International"},
			{Code: "mx", Name: "Mexico"},
			{Code: "no", Name: "Norge"},
			{Code: "fi", Name: "Suomi"},
			{Code: "uk", Name: "United Kingdom"},
		},
		Blocked: make(map[string]struct{}),
	}
	// ... rest of LoadConfig unchanged ...
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "true", "1", "t", "y", "yes":
		return true, nil
	case "false", "0", "f", "n", "no", "":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", v)
	}
}

func parseDuration(v string, fallback time.Duration) (time.Duration, error) {
	if v == "" {
		return fallback, nil
	}
	if seconds, err := strconv.Atoi(v); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	return fallback, fmt.Errorf("invalid duration value %q", v)
}
```

### main.go:39-73 — MODIFY

```go
var credentialPathPattern = regexp.MustCompile(`(?i)^(/feed/[^/]+/)[^/]+(/[^/]+\.xml.*)$`)

func redactURLString(raw string) string {
	return credentialPathPattern.ReplaceAllString(raw, "${1}REDACTED${2}")
}

type App struct {
	cfg          *Config
	logger       *slog.Logger
	limiter      *RateLimiter
	indexTmpl    *template.Template
	feedTmpl     *template.Template
	tokenCache   *podimo.FileCache
	podcastCache *podimo.FileCache
	headCache    *podimo.FileCache
	clients      *podimo.BoundedMap[string, *http.Client]
}

type RateLimiter struct {
	ips    *podimo.BoundedMap[string, []time.Time]
	window time.Duration
	max    int
}

func NewRateLimiter(window time.Duration, max int) *RateLimiter {
	return &RateLimiter{
		ips: podimo.NewBoundedMap[string, []time.Time](podimo.BoundedMapOptions{
			MaxSize:         10000,
			TTL:             window,
			CleanupInterval: window,
		}),
		window: window,
		max:    max,
	}
}

func (r *RateLimiter) Allow(ip string) bool {
	now := time.Now()
	var reqs []time.Time
	if v, ok := r.ips.Get(ip); ok {
		reqs = v
	}
	var valid []time.Time
	for _, t := range reqs {
		if now.Sub(t) < r.window {
			valid = append(valid, t)
		}
	}
	valid = append(valid, now)
	r.ips.Set(ip, valid, r.window)
	return len(valid) <= r.max
}
```

### main.go:149-154 — MODIFY

```go
	server := &http.Server{
		Addr:              cfg.BindHost,
		Handler:           router,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}
```

### main.go:174-181 — MODIFY

```go
func (a *App) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		urlStr := redactURLString(r.URL.String())
		a.logger.Debug("Request started", "method", r.Method, "url", urlStr, "ip", r.RemoteAddr, "ua", r.UserAgent())
		next.ServeHTTP(w, r)
		a.logger.Debug("Request completed", "method", r.Method, "url", urlStr, "duration", time.Since(start).Seconds())
	})
}
```

### main.go:318-374 — MODIFY

```go
	data, err := client.GetPodcasts(r.Context(), podcastID, a.cfg.PodcastCacheTime)
	if err != nil {
		if _, ok := err.(*podimo.PodcastNotFoundError); ok {
			http.Error(w, "Podcast not found. Are you sure you have the correct ID?", http.StatusNotFound)
			return
		}
		if _, ok := err.(*podimo.AuthenticationError); ok {
			authenticate(w)
			return
		}
		a.logger.Error("Podcast fetch error", "error", err)
		http.Error(w, "Something went wrong while fetching the podcasts", http.StatusInternalServerError)
		return
	}
```

### main.go:391-447 — MODIFY

```go
	data, err := client.GetPodcasts(r.Context(), podcastID, a.cfg.PodcastCacheTime)
	if err != nil {
		if _, ok := err.(*podimo.PodcastNotFoundError); ok {
			http.Error(w, "Podcast not found. Are you sure you have the correct ID?", http.StatusNotFound)
			return
		}
		if _, ok := err.(*podimo.AuthenticationError); ok {
			authenticate(w)
			return
		}
		a.logger.Error("Podcast fetch error", "error", err)
		http.Error(w, "Something went wrong while fetching the podcasts", http.StatusInternalServerError)
		return
	}
```

### main.go:488-506 — MODIFY

```go
func (a *App) getHTTPClient(key string) *http.Client {
	if client, ok := a.clients.Get(key); ok {
		return client
	}
	transport := &http.Transport{}
	if a.cfg.ZenRowsAPI != "" {
		zenrowsProxy := fmt.Sprintf("http://%s@proxy.zenrows.com:8000", a.cfg.ZenRowsAPI)
		proxyURL, _ := url.Parse(zenrowsProxy)
		transport.Proxy = http.ProxyURL(proxyURL)
	} else if a.cfg.HTTPProxy != "" {
		proxyURL, _ := url.Parse(a.cfg.HTTPProxy)
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 30 * time.Second}
	a.clients.Set(key, client, a.cfg.TokenCacheTime)
	return client
}
```

### podimo/client.go:247-316 — MODIFY

```go
		if err := c.graphql.Query(ctx, headers, query, variables, &result); err != nil {
			if _, ok := err.(*AuthenticationError); ok {
				return nil, err
			}
			var gqlErr *GQLError
			if errors.As(err, &gqlErr) {
				msg := strings.ToLower(gqlErr.Message)
				if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "not authorized") {
					return nil, NewAuthenticationError(gqlErr.Message)
				}
			}
			return nil, NewPodcastNotFoundError(fmt.Sprintf("Podcast %s not found or empty response", podcastID))
		}
```

### podimo/cache.go:10-39 — MODIFY

```go
type FileCache struct {
	dir      string
	keyLocks *BoundedMap[string, *sync.Mutex]
}

func NewFileCache(dir string) (*FileCache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &FileCache{
		dir: dir,
		keyLocks: NewBoundedMap[string, *sync.Mutex](BoundedMapOptions{
			MaxSize: 10000,
		}),
	}, nil
}

func (c *FileCache) getKeyLock(key string) *sync.Mutex {
	return c.keyLocks.GetOrSet(key, func() *sync.Mutex {
		return &sync.Mutex{}
	}, 0)
}
```

### podimo/graphql.go:43-88 — MODIFY

```go
func (c *GraphQLClient) Query(ctx context.Context, headers map[string]string, query string, variables map[string]interface{}, resp interface{}) error {
	body := gqlRequest{Query: query, Variables: variables}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json; charset=utf-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	const maxResponseSize = 10 * 1024 * 1024 // 10MB
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if len(raw) > maxResponseSize {
		return fmt.Errorf("graphql response exceeds %d bytes", maxResponseSize)
	}

	if res.StatusCode != http.StatusOK {
		body := raw
		if len(body) > 500 {
			body = body[:500]
		}
		return fmt.Errorf("graphql: non-200 status: %d, body: %s", res.StatusCode, body)
	}

	var gr gqlResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(gr.Errors) > 0 {
		return gr.Errors[0]
	}

	return json.Unmarshal(gr.Data, resp)
}



type GQLError struct {
	Message string `json:"message"`
}

func (e GQLError) Error() string { return "graphql: " + e.Message }

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GQLError      `json:"errors"`
}
```

### podimo/rss.go:66-105 — MODIFY

```go
	for _, chunk := range chunks(episodes, 5) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		items := make([]podcast.Item, len(chunk))
		var wg sync.WaitGroup
		for i, ep := range chunk {
			wg.Add(1)
			go func(idx int, raw interface{}) {
				defer wg.Done()
				if ctx.Err() != nil {
					return
				}
				episode, ok := raw.(map[string]interface{})
				if !ok {
					return
				}
				item, err := buildFeedItem(ctx, episode, locale, headCache, headCacheTTL, httpClient)
				if err == nil && item.Title != "" {
					items[idx] = item
				} else if err != nil && logger != nil {
					logger.Debug("Skipped episode", "error", err)
				}
			}(i, ep)
		}
		wg.Wait()
		for _, item := range items {
			if item.Title == "" {
				continue
			}
			if _, err := p.AddItem(item); err != nil && logger != nil {
				logger.Warn("Failed to add RSS item", "title", item.Title, "error", err)
			}
		}
	}
```

### podimo/rss.go:221-268 — MODIFY

```go
func URLHeadInfo(ctx context.Context, client *http.Client, id, urlStr string, headers map[string]string, headCache *FileCache, cacheTTL time.Duration) (string, string, error) {
	if entry, ok := headCache.Get(id); ok {
		if m, ok := entry.(map[string]interface{}); ok {
			length, _ := m["length"].(string)
			typ, _ := m["type"].(string)
			if length != "" && typ != "" {
				return length, typ, nil
			}
		}
	}

	retries := 3
	for attempt := 0; attempt < retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, urlStr, nil)
		if err != nil {
			return "0", "audio/mpeg", err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < retries-1 {
				select {
				case <-ctx.Done():
					return "0", "audio/mpeg", ctx.Err()
				case <-time.After(1 * time.Second):
				}
				continue
			}
			return "0", "audio/mpeg", err
		}

		contentLength := "0"
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			contentLength = cl
		}

		contentType := "audio/mpeg"
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			contentType = ct
		}

		resp.Body.Close()

		headCache.Set(id, map[string]interface{}{"length": contentLength, "type": contentType}, cacheTTL)
		return contentLength, contentType, nil
	}

	return "0", "audio/mpeg", fmt.Errorf("all retries failed for HEAD %s", urlStr)
}
```

## Slices

### Slice 1: BoundedMap foundation

**Files**: `podimo/boundedmap.go`, `podimo/boundedmap_test.go`

#### Automated Verification:
- [ ] `go test ./podimo/...` passes with new `BoundedMap` tests
- [ ] `go vet ./...` passes
- [ ] `BoundedMap` supports `Get`, `Set`, TTL expiration, max-size LRU eviction, and background cleanup

#### Manual Verification:
- [ ] Review `BoundedMap` API surface — idiomatic generics usage, thread-safe

### Slice 2: Config strictness + server hardening

**Files**: `config.go`, `main.go`

#### Automated Verification:
- [ ] `go test ./...` passes with config validation tests
- [ ] `TestLoadConfig_InvalidBool`/`TestLoadConfig_InvalidDuration` fail as expected
- [ ] Server starts with timeouts: `ReadTimeout=30s`, `WriteTimeout=60s`, `IdleTimeout=120s`, `ReadHeaderTimeout=10s`, `MaxHeaderBytes=1MB`

#### Manual Verification:
- [ ] Verify `PODIMO_BIND_HOST=malformed` produces clear startup error
- [ ] Verify DEBUG=true with invalid TOKEN_CACHE_TIME produces clear startup error

### Slice 3: Error taxonomy + logging security

**Files**: `podimo/client.go`, `main.go`

#### Automated Verification:
- [ ] `go test ./...` passes with updated error handling tests
- [ ] `TestHandleFeed_AuthError` returns 401 with `AuthenticationError`
- [ ] `TestHandleFeedPath_AuthError` returns 401 with `AuthenticationError`
- [ ] `TestLoggingMiddleware_Redaction` confirms REDACTED in output
- [ ] `TestPodimoClient_GetPodcasts` no longer relies on string matching for auth errors

#### Manual Verification:
- [ ] Review `AuthenticationError` wrapping in `GraphQLClient.Query` and `GetPodcasts`

### Slice 4: Memory leak fixes

**Files**: `main.go`, `podimo/cache.go`

#### Automated Verification:
- [ ] `go test ./...` passes with updated rate limiter and cache tests
- [ ] `TestRateLimiter_IPCleanup` confirms expired IPs are removed
- [ ] `TestFileCache_MutexBounded` confirms keyLocks does not grow unbounded
- [ ] `TestHTTPClient_CacheBounded` confirms old clients are evicted

#### Manual Verification:
- [ ] Verify `BoundedMap` background cleanup goroutine starts and stops cleanly
- [ ] Verify rate limiter handles burst correctly after migration

### Slice 5: GraphQL + RSS transport hardening

**Files**: `podimo/graphql.go`, `podimo/rss.go`

#### Automated Verification:
- [ ] `go test ./...` passes with updated GraphQL and RSS tests
- [ ] `TestGraphQLClient_Query_LargeResponse` returns error before OOM
- [ ] `TestPodcastsToRss_ContextCancel` stops processing mid-chunk
- [ ] `TestURLHeadInfo_ContextCancel` returns immediately on cancelled context during retry sleep

#### Manual Verification:
- [ ] Verify large GraphQL response (>10MB) is rejected with clear error
- [ ] Verify context cancellation stops RSS generation promptly

## Desired End State

After implementation, the service:

1. Starts with clear errors if any environment variable is invalid.
2. Runs an `http.Server` hardened against slowloris and resource exhaustion.
3. Logs request URLs with credentials redacted.
4. Returns HTTP 401 for authentication failures and 404 for missing podcasts.
5. Classifies GraphQL errors via structured wrapping, not string matching.
6. Limits response sizes to prevent OOM.
7. Respects request cancellation during RSS generation and HEAD retries.
8. Maintains bounded memory usage regardless of runtime duration or user count.

Example usage of `BoundedMap`:

```go
// In main.go — replaces App.clients
clients := podimo.NewBoundedMap[string, *http.Client](podimo.BoundedMapOptions{
    MaxSize: 100,
    TTL:     5 * 24 * time.Hour,
})
```

## File Map

- `podimo/boundedmap.go`          # NEW — Generic TTL/LRU bounded map
- `podimo/boundedmap_test.go`     # NEW — Tests for BoundedMap
- `config.go`                      # MODIFY — parseBool/parseDuration return errors; strict validation
- `main.go`                        # MODIFY — server timeouts, URL redaction, auth 401, BoundedMap for clients+rate limiter
- `podimo/client.go`               # MODIFY — Replace string-matching error classification
- `podimo/cache.go`                # MODIFY — BoundedMap for keyLocks
- `podimo/graphql.go`              # MODIFY — io.LimitReader response size cap
- `podimo/rss.go`                  # MODIFY — Context cancellation checks

## Ordering Constraints

- Slice 1 (BoundedMap) must precede Slice 4 (memory leak fixes) because Slice 4 depends on the `BoundedMap` type.
- Slices 2, 3, and 5 have no inter-dependencies and could run in parallel with each other (but are sequenced for design generation).
- Slice 4 must follow Slice 1.

## Verification Notes

- Precedent from Python-era timeout commits (`7a78c6b`, `2474fec`): timeout additions must be validated with server startup tests.
- Precedent from error handling commits (`28f972e`): adding `errors.As` support to custom errors may require test updates if tests use direct type assertions.
- `FileCache.keyLocks` migration to `BoundedMap` must preserve the per-key mutual exclusion guarantee. LRU eviction of an active mutex is extremely unlikely with a large max size, but tests should verify correctness under contention.
- `URLHeadInfo` retry sleep cancellation: use `select { case <-ctx.Done(): ... case <-time.After(1 * time.Second): ... }` instead of `time.Sleep`.
- The `loggingMiddleware` URL redaction must not break URL parsing for non-feed paths.

## Performance Considerations

- `BoundedMap` background cleanup runs every cleanup interval (e.g., 1 minute) and scans all entries. With default max sizes (~100-10000), this is O(n) but trivial for the expected scale.
- Server `IdleTimeout` (120s) may terminate idle keep-alive connections. This is acceptable for a self-hosted RSS service where podcast apps poll intermittently.
- `io.LimitReader` with 10MB cap provides defense-in-depth without affecting normal GraphQL responses (typical responses are <1MB).
- Context cancellation in RSS saves goroutine churn and HEAD requests for cancelled client connections.

## Migration Notes

- No external schema or data migration required. This is a pure code hardening pass.
- Backwards compatibility: `parseBool`/`parseDuration` signature changes are internal to `config.go`.
- The `BoundedMap` utility is additive; existing maps are replaced inline.
- Existing cache files on disk are unaffected by the `FileCache.keyLocks` change.

## Pattern References

- `podimo/cache.go:30-39` — Per-key mutex pattern to model for `BoundedMap` thread safety.
- `main.go:174-180` — `loggingMiddleware` pattern to extend with URL redaction.
- `main.go:318-374` — `handleFeed` error branching pattern to extend with `AuthenticationError` → 401.
- `podimo/client.go:47-54` — Custom error struct pattern; needs `Unwrap()` for `errors.As` support.
- `podimo/graphql.go:43-82` — GraphQL response handling pattern to extend with `io.LimitReader`.

## Developer Context

**Q (`config.go:127` and `config.go:136`): Should invalid env values cause startup failure to surface misconfiguration, or log a warning and continue with defaults?**
A: Startup failure (Recommended) — hard fail on invalid env values with a clear error message.

**Q (`main.go:174-180`): Should DEBUG logs redact the password portion from `/feed/<username>/<password>/...` URLs, or exclude feed endpoints from DEBUG URL logging entirely?**
A: Redact password in path — keep logging feed URLs but strip the password segment.

**Q: Unbounded maps fix strategy — shared bounded utility or ad-hoc?**
A: Shared bounded utility (Recommended).

**Q: RateLimiter IP extraction — X-Forwarded-For or RemoteAddr only?**
A: Keep RemoteAddr only (Recommended).

**Q: RSS concurrency control — context checks only or global semaphore?**
A: Context cancellation checks only (Recommended).

## Design History

- Slice 1: BoundedMap foundation — approved as generated
- Slice 2: Config strictness + server hardening — approved as generated
- Slice 3: Error taxonomy + logging security — approved as generated
- Slice 4: Memory leak fixes — approved as generated
- Slice 5: GraphQL + RSS transport hardening — approved as generated

## References

- `.rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md` — Source research artifact
- `.rpiv/artifacts/research/2026-05-22_15-54-18_improvement-opportunities.md` — Python baseline issues
- `AGENTS.md` — Project context and known gotchas
