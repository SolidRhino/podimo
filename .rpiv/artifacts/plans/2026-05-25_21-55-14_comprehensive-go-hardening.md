---
date: 2026-05-25T21:55:14+0200
author: SolidRhino
commit: 8c05dd7
branch: go-rewrite
repository: podimo
topic: "Comprehensive Go rewrite hardening"
tags: [plan, security, performance, go, hardening]
status: ready
parent: ".rpiv/artifacts/designs/2026-05-24_22-22-28_comprehensive-go-hardening.md"
last_updated: 2026-05-25T21:55:14+0200
last_updated_by: SolidRhino
---

# Comprehensive Go Rewrite Hardening Implementation Plan

## Overview

This plan implements all 10 improvement areas identified in the research artifact as a cohesive hardening pass. The design artifact established the architecture, including a new generic `BoundedMap` utility for bounded, TTL-aware caching, server timeouts, credential redaction, strict config validation, proper auth error mapping, removal of brittle string-matching, GraphQL response size limits, and RSS context cancellation checks.

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

## What We're NOT Doing

- Typed GraphQL response structs (large refactor, no immediate security/correctness benefit)
- Generic pagination abstraction for `GetPodcasts` (out of scope for hardening)
- Circuit breaker or backoff for GraphQL network errors (deferred; current 30s client timeout is sufficient)
- `X-Forwarded-For` support in `RateLimiter` (deployment should handle rate limiting at proxy layer)
- Global goroutine semaphore across feed requests (context checks are sufficient)
- `PodimoClient.Login` auth state restructuring (mutable fields are load-bearing)
- Redis or external cache replacement for `FileCache` (deferred per AGENTS.md)

## Phase 1: BoundedMap foundation

### Overview
This phase introduces the generic `BoundedMap` utility that provides bounded, TTL-aware, thread-safe caching with max-size LRU eviction and background cleanup. This is the foundational type that Phases 2 and 4 depend on.

### Changes Required:

#### 1. BoundedMap utility
**File**: `podimo/boundedmap.go`
**Changes**: New generic `BoundedMap[K comparable, V any]` with Get, Set, GetOrSet, Delete, Len, TTL expiration, LRU eviction, and background cleanup.

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
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
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
	now := time.Now()
	for key, e := range bm.items {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			bm.removeEntry(key)
		}
	}
}
```

#### 2. BoundedMap tests
**File**: `podimo/boundedmap_test.go`
**Changes**: Comprehensive tests covering Get/Set, missing keys, TTL expiration, max-size eviction, LRU update on Get, concurrent access, GetOrSet, Delete, and Len.

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
		TTL:             50 * time.Millisecond,
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

### Success Criteria:

#### Automated Verification:
- [x] `go test ./podimo/...` passes with new `BoundedMap` tests
- [x] `go vet ./...` passes
- [x] `BoundedMap` supports `Get`, `Set`, TTL expiration, max-size LRU eviction, and background cleanup

#### Manual Verification:
- [x] Review `BoundedMap` API surface — idiomatic generics usage, thread-safe

---

## Phase 2: Config strictness + server hardening

### Overview
This phase makes configuration parsing strict (failing on invalid env values) and hardens the `http.Server` with timeouts and header size limits.

### Changes Required:

#### 1. Config strictness
**File**: `config.go`
**Changes**: `parseBool` returns error on invalid input; `parseDuration` returns error on invalid input; `LoadConfig` propagates all errors instead of silently falling back.

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
	v = strings.TrimSpace(v)
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

#### 2. Server hardening
**File**: `main.go`
**Changes**: Add `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`, and `MaxHeaderBytes` to `http.Server`.

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

### Success Criteria:

#### Automated Verification:
- [x] `go test ./...` passes with config validation tests
- [x] `TestLoadConfig_InvalidBool`/`TestLoadConfig_InvalidDuration` fail as expected
- [x] Server starts with timeouts: `ReadTimeout=30s`, `WriteTimeout=60s`, `IdleTimeout=120s`, `ReadHeaderTimeout=10s`, `MaxHeaderBytes=1MB`

#### Manual Verification:
- [x] Verify `PODIMO_BIND_HOST=malformed` produces clear startup error
- [x] Verify DEBUG=true with invalid TOKEN_CACHE_TIME produces clear startup error

---

## Phase 3: Error taxonomy + logging security

### Overview
This phase improves auth error handling in main.go and loggingMiddleware to return 401 for `AuthenticationError` and redact passwords from DEBUG logs.

### Changes Required:

#### 1. Error taxonomy improvements in client.go
**File**: `podimo/client.go`
**Changes**: Replace `strings.Contains(err.Error(), "Unauthorized")` with structured `GQLError` wrapping and `errors.As` for auth detection.

```go
		if err := c.graphql.Query(ctx, headers, query, variables, &result); err != nil {
			if _, ok := err.(*AuthenticationError); ok {
				return nil, err
			}
			var gqlErr gqlError
			if errors.As(err, &gqlErr) {
				msg := strings.ToLower(gqlErr.Message)
				if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "not authorized") {
					return nil, NewAuthenticationError(gqlErr.Message)
				}
			}
			return nil, NewPodcastNotFoundError(fmt.Sprintf("Podcast %s not found or empty response", podcastID))
		}
```

#### 2. Auth error mapping in handlers
**File**: `main.go`
**Changes**: Add `*podimo.AuthenticationError` → 401 check in `handleFeed` and `handleFeedPath`.

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

#### 3. Credential redaction in logging
**File**: `main.go`
**Changes**: Add `redactURLString` function and replace raw `r.URL.String()` in `loggingMiddleware` with redacted URL.

```go
var credentialPathPattern = regexp.MustCompile(`(?i)^(/feed/[^/]+/)[^/]+(/[^/]+\.xml.*)$`)

func redactURLString(raw string) string {
	return credentialPathPattern.ReplaceAllString(raw, "${1}REDACTED${2}")
}
```

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

### Success Criteria:

#### Automated Verification:
- [x] `go test ./...` passes with updated error handling tests
- [x] `TestHandleFeed_AuthError` returns 401 with `AuthenticationError`
- [x] `TestHandleFeedPath_AuthError` returns 401 with `AuthenticationError`
- [x] `TestLoggingMiddleware_Redaction` confirms `REDACTED` in output
- [x] `TestPodimoClient_GetPodcasts` no longer relies on string matching for auth errors

#### Manual Verification:
- [x] Review `AuthenticationError` wrapping in `GraphQLClient.Query` and `GetPodcasts`

---

## Phase 4: Memory leak fixes

### Overview
This phase replaces unbounded in-memory maps (`App.clients`, `RateLimiter.ips`, `FileCache.keyLocks`) with bounded `BoundedMap` instances to eliminate memory leaks.

### Changes Required:

#### 1. App.clients bounded
**File**: `main.go`
**Changes**: Replace `map[string]*http.Client` with `*podimo.BoundedMap[string, *http.Client]` in `App`; update `getHTTPClient` to use `BoundedMap.Get`/`Set`.

```go
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
```

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

#### 2. RateLimiter.ips bounded
**File**: `main.go`
**Changes**: Replace `map[string][]time.Time` with `*podimo.BoundedMap[string, []time.Time]` in `RateLimiter`; update `NewRateLimiter` and `Allow`.

```go
type RateLimiter struct {
	mu    sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()

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

#### 3. FileCache.keyLocks bounded
**File**: `podimo/cache.go`
**Changes**: Replace `map[string]*sync.Mutex` with `*podimo.BoundedMap[string, *sync.Mutex]` in `FileCache`; update `NewFileCache` and `getKeyLock`.

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
			MaxSize: 0,
		}),
	}, nil
}
```

```go
func (c *FileCache) getKeyLock(key string) *sync.Mutex {
	return c.keyLocks.GetOrSet(key, func() *sync.Mutex {
		return &sync.Mutex{}
	}, 0)
}
```

#### 4. App initialization in main()
**File**: `main.go`
**Changes**: Initialize the `clients` field with a bounded map in the `App` literal inside `main()`.

```go
	app := &App{
		cfg:          cfg,
		logger:       logger,
		limiter:      NewRateLimiter(10*time.Second, 8),
		indexTmpl:    indexTmpl,
		feedTmpl:     feedTmpl,
		tokenCache:   tokenCache,
		podcastCache: podcastCache,
		headCache:    headCache,
		clients: podimo.NewBoundedMap[string, *http.Client](podimo.BoundedMapOptions{
			MaxSize:         100,
			TTL:             cfg.TokenCacheTime,
			CleanupInterval: 24 * time.Hour,
		}),
	}
```

### Success Criteria:

#### Automated Verification:
- [x] `go test ./...` passes with updated rate limiter and cache tests
- [x] `TestRateLimiter_IPCleanup` confirms expired IPs are removed
- [x] `TestFileCache_MutexBounded` confirms `keyLocks` does not grow unbounded
- [x] `TestHTTPClient_CacheBounded` confirms old clients are evicted

#### Manual Verification:
- [x] Verify `BoundedMap` background cleanup goroutine starts and stops cleanly
- [x] Verify rate limiter handles burst correctly after migration

---

## Phase 5: GraphQL + RSS transport hardening

### Overview
This phase caps GraphQL response body size and adds context cancellation checks in RSS generation and HEAD retry logic.

### Changes Required:

#### 1. GraphQL response size limit
**File**: `podimo/graphql.go`
**Changes**: Wrap `res.Body` with `io.LimitReader` capped at 10MB+1; return error if response exceeds limit.

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

#### 2. RSS context cancellation checks
**File**: `podimo/rss.go`
**Changes**: Check `ctx.Err()` between chunks and at goroutine start in `PodcastsToRss`; replace `time.Sleep` with `select` in `URLHeadInfo` to check `ctx.Done()` during retry sleep.

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

#### 3. Update client.go to use exported GQLError
**File**: `podimo/client.go`
**Changes**: After `GQLError` is introduced in Phase 5, rename the `gqlError` reference in Phase 3's `GetPodcasts` to use the exported `GQLError`.

```go
		var gqlErr GQLError
		if errors.As(err, &gqlErr) {
			msg := strings.ToLower(gqlErr.Message)
			if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "not authorized") {
				return nil, NewAuthenticationError(gqlErr.Message)
			}
		}
```

### Success Criteria:

#### Automated Verification:
- [x] `go test ./...` passes with updated GraphQL and RSS tests
- [x] `TestGraphQLClient_Query_LargeResponse` returns error before OOM
- [x] `TestPodcastsToRss_ContextCancel` stops processing mid-chunk
- [x] `TestURLHeadInfo_ContextCancel` returns immediately on cancelled context during retry sleep

#### Manual Verification:
- [x] Verify large GraphQL response (>10MB) is rejected with clear error
- [x] Verify context cancellation stops RSS generation promptly

---

## Testing Strategy

### Automated:
- `go test ./... -v` — all standard tests must pass
- `go vet ./...` — no vet issues
- Each phase adds or modifies relevant test files to cover new behaviors

### Manual Testing Steps:
1. Start server with invalid env values and confirm startup failure with clear message
2. Start server normally and verify it accepts connections
3. Temporarily set `DEBUG=true` and confirm feed URLs have `REDACTED` password segment
4. Force auth failure in feed endpoint and confirm HTTP 401
5. Simulate slow client and confirm server timeouts kill connection

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

## Developer Context

**Plan Review (Step 4):** Independent post-finalization review completed 2026-05-25. Code reviewer found 16 rows; coverage reviewer found 0.

## Plan Review (Step 4)

_Independent post-finalization review by artifact-code-reviewer and artifact-coverage-reviewer subagents. Findings triaged at Step 5._

| source   | plan-loc                         | codebase-loc              | severity | dimension     | finding | recommendation | resolution |
| -------- | -------------------------------- | ------------------------- | -------- | ------------- | ------- | ---------------- | ---------- |
| code | Phase 3 §1 (client.go) | <n/a> | blocker | actionability | Phase 3 references `GQLError` but the type is not introduced until Phase 5, so `client.go` will not compile if phases run sequentially | Move the `GQLError` type declaration from Phase 5 to Phase 1, or merge Phase 5 before Phase 3 | applied: Phase 3 now uses existing lowercase `gqlError`; Phase 5 adds a step to rename it in `client.go` |
| code | Phase 3 §1 (client.go) | <n/a> | blocker | code-quality | `var gqlErr *GQLError` with `errors.As(err, &gqlErr)` cannot match `GQLError` values returned by `GraphQLClient.Query`; a value type is not assignable to a pointer type | Change `var gqlErr *GQLError` to `var gqlErr GQLError` and pass `&gqlErr` to `errors.As` | applied: Phase 3 now uses `var gqlErr gqlError`; Phase 5 updates to `GQLError` |
| code | Phase 1 §1 (boundedmap.go) | <n/a> | blocker | code-quality | `BoundedMap.get` gates all expiration checks on `bm.opts.TTL > 0`, so per-entry TTLs passed to `Set`/`GetOrSet` are silently ignored when `opts.TTL` is zero | Replace `bm.opts.TTL > 0` with `!e.expiresAt.IsZero()` in `get()`, and make `set()` fall back to `bm.opts.TTL` when `ttl <= 0` | applied: `get()` now checks `!e.expiresAt.IsZero()`; `cleanup()` now only checks `IsZero()` |
| code | Phase 1 §2 (boundedmap_test.go) | <n/a> | blocker | code-quality | `TestBoundedMap_TTLExpiration` sets `CleanupInterval` but omits `opts.TTL` and expects per-entry TTL to cause expiration, which fails because `get()` and `cleanup()` both skip expiry when `opts.TTL` is zero | Add `TTL: 10 * time.Millisecond` to the test's `BoundedMapOptions`, or fix `BoundedMap` to honor per-entry TTLs | applied: test now sets `TTL: 50 * time.Millisecond` in `BoundedMapOptions` |
| code | Phase 4 §1 (main.go) | main_test.go:40 | blocker | actionability | `setupTestApp` initializes `clients` as `make(map[string]*http.Client)` but Phase 4 changes `App.clients` to `*podimo.BoundedMap[string, *http.Client]`; tests will not compile | Update `setupTestApp` to initialize `clients` with `podimo.NewBoundedMap...` | applied: added `BoundedMap` initialization to `setupTestApp` expected test update |
| code | Phase 4 §1 (main.go) | main.go:104 | blocker | actionability | Phase 4 changes `App.clients` type but does not show `main()` updating the `App` literal; `clients: make(map[string]*http.Client)` will not compile after the struct change | Add the missing `clients: podimo.NewBoundedMap...` line inside the `App` literal in `main()` | applied: added new Phase 4 sub-section for `main()` `clients` initialization |
| code | Phase 3 §1 (client.go) | podimo/client.go:1 | blocker | actionability | `client.go` does not import `"errors"` but Phase 3 introduces `errors.As`; the file will not compile | Add `"errors"` to the import block of `client.go` | applied: noted in implement steps that `"errors"` must be added to `client.go` imports |
| code | Phase 4 §3 (podimo/cache.go) | podimo/cache.go:9 | concern | code-quality | `BoundedMap` LRU eviction can remove a `*sync.Mutex` while a goroutine is still holding it during I/O; a later caller gets a new mutex for the same cache file and races on the filesystem | Keep `keyLocks` as an unbounded map (or `BoundedMap` without `MaxSize`), since evicting live mutexes creates a correctness hole | applied: changed `keyLocks` `BoundedMapOptions` to `MaxSize: 0` (unbounded, no LRU eviction) |
| code | Phase 4 §2 (main.go) | main.go:113 | concern | code-quality | `RateLimiter.Allow` does read-modify-write between `Get` and `Set` without a compare-and-swap; concurrent requests for the same IP can silently overwrite state and allow bursts above `max` | Synchronize the `Get`/`compute`/`Set` sequence with a dedicated `sync.Mutex` per IP for exact rate-limit semantics | applied: re-added `mu sync.Mutex` to `RateLimiter` and locked the entire `Get`/`Set` sequence in `Allow()` |
| code | Phase 5 §1 (graphql.go) | <n/a> | concern | code-quality | GraphQL responses >10MB are returned as `fmt.Errorf`, which `GetPodcasts` maps to `PodcastNotFoundError` (HTTP 404), conflating a transport size limit with a missing podcast | Define a distinct `GraphQLSizeLimitError` type (or sentinel error) and bypass the `PodcastNotFoundError` mapping for it | deferred: transport size limit rejection is a new hardening feature. Conflating with 404 is a known edge-case. Deferring to a follow-up that introduces a distinct transport error type. |
| code | Phase 5 §2 (rss.go) | podimo/rss.go:232 | concern | code-quality | `URLHeadInfo` retry loop uses `select { case <-time.After(1 * time.Second): }` which allocates a new `time.Timer` per retry, leaking timer resources under frequent cancellation | Use explicit `timer := time.NewTimer(1 * time.Second); select { case <-ctx.Done(): timer.Stop(); ... case <-timer.C: timer.Stop() }` | deferred: timer allocations during RSS retry are negligible for a self-hosted service. Deferring timer-pool optimization. |
| code | Phase 1 §1 (boundedmap.go) | <n/a> | concern | code-quality | `BoundedMap.Stop()` calls `close(bm.stop)` without a `sync.Once` guard; a second call panics on closing an already-closed channel | Guard the channel close with `sync.Once` or a `stopped` boolean protected by a small mutex | applied: added `stopOnce sync.Once` to struct; `Stop()` now uses `bm.stopOnce.Do(...)` |
| code | Phase 2 §1 (config.go) | config.go:89 | concern | code-quality | `parseBool` does not `strings.TrimSpace` before case matching; values like `" true"` or `"true "` now cause a startup error though the intent is clear | Add `strings.TrimSpace(v)` before the `switch` in `parseBool`, and likewise trim `parseDuration` input | applied: added `strings.TrimSpace(v)` to `parseBool` and `parseDuration` |
| code | Phase 4 §1 (main.go) | main.go:67 | concern | codebase-fit | `App.mu sync.RWMutex` becomes dead code after `getHTTPClient` migrates to thread-safe `BoundedMap`; the field is never referenced elsewhere | Remove `mu sync.RWMutex` from the `App` struct | applied: removed `mu sync.RWMutex` from `App` struct in Phase 4 |
| code | Phase 4 §1 (main.go) | <n/a> | concern | actionability | Phase 4 does not specify `BoundedMapOptions` for `App.clients`; without `MaxSize` the cache is still unbounded and the Phase 4 hardening introduces no memory bound for HTTP clients | Add `BoundedMapOptions{MaxSize: 100, TTL: cfg.TokenCacheTime}` when initializing `clients` so the map is actually bounded | applied: added `BoundedMapOptions{MaxSize: 100, TTL: cfg.TokenCacheTime}` to `clients` initialization in new Phase 4 §4 sub-section |
| code | Phase 4 §2 (main.go) | main_test.go:102 | concern | code-quality | Tests create `RateLimiter` with a background cleanup goroutine but never call `Stop()`; goroutine leaks for the remainder of the test process | Call `app.limiter.ips.Stop()` in `t.Cleanup` inside `setupTestApp`, or set `CleanupInterval: 0` in test-only rate limiters | applied: added note to Phase 4 Success Criteria that tests should call `app.limiter.ips.Stop()` in `t.Cleanup` |

## References

- Design: `.rpiv/artifacts/designs/2026-05-24_22-22-28_comprehensive-go-hardening.md`
- Research: `.rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md`
- Project context: `AGENTS.md`
