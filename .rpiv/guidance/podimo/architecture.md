# podimo/ — Podimo API Adapter Layer

## Responsibility
Encapsulates the Podimo GraphQL boundary: 3-step auth flow, paginated episode fetching, search, subscription listing, RSS generation, and disk-backed TTL caching. Exported error types let HTTP handlers map domain failures to semantic status codes without string parsing.

## Dependencies
- Standard library + `github.com/eduncan911/podcast` for RSS XML.
- Consumer: `main` package only. `podimo/` never imports `main`.

## Module Structure
```
podimo/
├── client.go / client_test.go  → PodimoClient: auth, queries, pagination
├── graphql.go / graphql_test.go → GraphQLClient: HTTP POST wrapper
├── rss.go / rss_test.go         → RSS builder + audio URL extraction
├── cache.go / cache_test.go     → FileCache: disk-backed TTL
└── boundedmap.go / boundedmap_test.go → BoundedMap: generic in-memory LRU + TTL
```

## Error Taxonomy
Concrete errors embed `PodimoError`. Type-assert in HTTP handlers for 401/404 mapping.

```go
type PodimoError struct{ Message string }
func (e PodimoError) Error() string { return e.Message }

type PodcastNotFoundError struct{ PodimoError }
type AuthenticationError  struct{ PodimoError }

// Handler branch — never match on string contents
if _, ok := err.(*podimo.PodcastNotFoundError); ok {
    http.Error(w, "Not found", http.StatusNotFound)
}
```

## GraphQL Post + Untyped Response Bridge
Queries are inline triple-quoted strings. Responses are unmarshaled into `map[string]interface{}`, then navigated with defensive type assertions.

```go
var result map[string]interface{}
err := c.graphql.Query(ctx, headers, query, variables, &result)
podcast, ok := result["podcast"].(map[string]interface{})
```

Paginated endpoints stitch pages into a single result: first page captures metadata; subsequent pages append only the `episodes` slice.

## Stateful Client with Lazy Login
`PodimoClient` restores its bearer token from `FileCache` on construction. `Login()` returns immediately when `Token()` is already populated.

```go
client, _ := podimo.NewPodimoClient(user, pass, region, locale,
    graphql, tokenCache, podcastCache, logger)
if client.Token() != "" {
    return client, nil // cache hit; skip remote login
}
token, err := client.Login(ctx)
```

Token caching is optional — pass `nil` for `tokenCache` when `StoreTokensOnDisk` is `false`.

## File Cache
Per-key JSON files (`<key>.json`) with embedded `expires_at`. Per-key mutex isolates concurrent writes to different keys.

```go
cache.Set(key, value, ttl)
if v, ok := cache.Get(key); ok { /* type-assert expected shape */ }
```

## BoundedMap
Generic in-memory LRU cache with optional TTL and background cleanup. Used for per-user `http.Client` pools and rate-limiter IP tracking. Background cleanup goroutines are lightweight and exit naturally on process termination.

```go
m := podimo.NewBoundedMap[string, *http.Client](podimo.BoundedMapOptions{
    MaxSize: 100,
    TTL:     time.Hour,
})
```

## Parallel Chunked RSS Workers
Each episode requires an HTTP HEAD call for enclosure metadata. Episodes process in chunks of 5 with `sync.WaitGroup`. HEAD failures fallback to safe defaults (`audio/mpeg`, length 0) so one bad episode does not abort the feed.

```go
for _, chunk := range chunks(episodes, 5) {
    items := make([]podcast.Item, len(chunk))
    // goroutine per episode → buildFeedItem(ctx, ep) → items[idx] = item
}
```

<important if="you are adding a new GraphQL query">
1. Write an inline triple-quoted query string. Every declared `$variable` must be consumed in the query body.
2. Pass `map[string]interface{}` to `c.graphql.Query(ctx, headers, query, variables, &result)`.
3. Type-assert every nested field with two-step `x, ok := y.(T)`.
4. Map upstream auth failures to `AuthenticationError`; missing resources to `PodcastNotFoundError`.
5. Cache the stitched result (not per-page fragments) when applicable.
6. Add mock responses to `mockGraphQLServer` in `client_test.go`.
</important>

<important if="you are adding a new cache consumer">
1. Accept `*podimo.FileCache` in the constructor or function signature.
2. Choose a stable string key (often a SHA-256 hash or UUID).
3. Call `Get(key)` at the start of the operation; if hit, return early.
4. On miss, perform the work, then `Set(key, result, ttl)`.
5. Ensure cached values are JSON-serializable (maps, slices, scalars).
</important>
