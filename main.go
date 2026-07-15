package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SolidRhino/podimo-rss/podimo"
	"github.com/go-chi/chi/v5"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

var podcastIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var credentialPathPattern = regexp.MustCompile(`(?i)^(/feed/)[^/]+/[^/]+(/[^/]+\.xml.*)$`)
var urlUUIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func redactURLString(raw string) string {
	return credentialPathPattern.ReplaceAllString(raw, "${1}REDACTED/REDACTED${2}")
}

type App struct {
	cfg          *Config
	logger       *slog.Logger
	limiter      *RateLimiter
	templates    *template.Template
	tokenCache   *podimo.FileCache
	podcastCache *podimo.FileCache
	headCache    *podimo.FileCache
	clients      *podimo.BoundedMap[string, *http.Client]
}

type RateLimiter struct {
	mu     sync.Mutex
	ips    *podimo.BoundedMap[string, *slidingWindow]
	window time.Duration
	max    int
}

// slidingWindow is a fixed-window counter that slides forward in time. When the
// current window elapses the count resets. This is O(1) per Allow call with no
// slice scan or reallocation.
type slidingWindow struct {
	count  int
	starts time.Time
}

func NewRateLimiter(window time.Duration, max int) *RateLimiter {
	return &RateLimiter{
		ips: podimo.NewBoundedMap[string, *slidingWindow](podimo.BoundedMapOptions{
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
	w, ok := r.ips.Peek(ip)
	if !ok || w == nil {
		w = &slidingWindow{starts: now}
	}
	if now.Sub(w.starts) >= r.window {
		w.count = 0
		w.starts = now
	}
	w.count++
	r.ips.Set(ip, w, r.window)
	return w.count <= r.max
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	configFile := flag.String("config", "", "path to YAML config file")
	flag.Parse()

	cfg, err := LoadConfig(*configFile)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{
					Key:   slog.TimeKey,
					Value: slog.StringValue(a.Value.Time().UTC().Format("2006-01-02T15:04:05Z")),
				}
			}
			return a
		},
	}))

	tmpl, err := template.ParseFS(templatesFS, "templates/index.html", "templates/feed_location.html", "templates/partials/*.html")
	if err != nil {
		logger.Error("Failed to parse templates", "error", err)
		os.Exit(1)
	}

	tokenCache, err := podimo.NewSecureFileCache(filepath.Join(cfg.CacheDir, "tokens_cache"))
	if err != nil {
		logger.Error("Failed to create token cache", "error", err)
		os.Exit(1)
	}

	podcastCache, err := podimo.NewFileCache(filepath.Join(cfg.CacheDir, "podcast_cache"))
	if err != nil {
		logger.Error("Failed to create podcast cache", "error", err)
		os.Exit(1)
	}

	headCache, err := podimo.NewFileCache(filepath.Join(cfg.CacheDir, "head_cache"))
	if err != nil {
		logger.Error("Failed to create head cache", "error", err)
		os.Exit(1)
	}

	app := &App{
		cfg:          cfg,
		logger:       logger,
		limiter:      NewRateLimiter(10*time.Second, 8),
		templates:    tmpl,
		tokenCache:   tokenCache,
		podcastCache: podcastCache,
		headCache:    headCache,
		clients: podimo.NewBoundedMap[string, *http.Client](podimo.BoundedMapOptions{
			MaxSize:         100,
			TTL:             cfg.TokenCacheTime,
			CleanupInterval: 24 * time.Hour,
			OnEvict: func(v any) {
				if c, ok := v.(*http.Client); ok {
					c.CloseIdleConnections()
				}
			},
		}),
	}

	router := app.setupRoutes()

	logger.Info("Starting server", "bind", cfg.BindHost)

	server := &http.Server{
		Addr:              cfg.BindHost,
		Handler:           router,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}

	if err := server.ListenAndServe(); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func (a *App) setupRoutes() chi.Router {
	r := chi.NewRouter()
	r.Use(a.loggingMiddleware)
	r.NotFound(a.notFoundHandler)

	r.Handle("/static/*", http.FileServer(http.FS(staticFS)))

	r.Get("/health", a.handleHealth)
	r.With(a.rateLimitMiddleware).Get("/search", a.handleSearch)
	r.With(a.rateLimitMiddleware).Get("/subscriptions", a.handleSubscriptions)
	r.Get("/", a.handleIndex)
	r.Post("/", a.handleIndex)
	r.With(a.rateLimitMiddleware).Get("/feed/{podcast_id}.xml", a.handleFeed)
	r.With(a.rateLimitMiddleware).Get("/feed/{username}/{password}/{podcast_id}.xml", a.handleFeedPath)
	return r
}

func (a *App) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		a.logger.Debug("Request started", "method", r.Method, "url", redactURLString(r.URL.String()), "ip", r.RemoteAddr, "ua", r.UserAgent())
		next.ServeHTTP(w, r)
		a.logger.Debug("Request completed", "method", r.Method, "url", redactURLString(r.URL.String()), "duration", time.Since(start).Seconds())
	})
}

func (a *App) clientIP(r *http.Request) string {
	if a.cfg.TrustedProxyHeader != "" {
		if h := r.Header.Get(a.cfg.TrustedProxyHeader); h != "" {
			// X-Forwarded-For may carry a comma-separated client chain; the first
			// entry is the original client.
			if i := strings.IndexByte(h, ','); i >= 0 {
				return strings.TrimSpace(h[:i])
			}
			return strings.TrimSpace(h)
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *App) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.limiter.Allow(a.clientIP(r)) {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","service":"podimo-rss"}`))
}

// runHealthcheck probes the local /health endpoint. Returns 0 on HTTP 200, 1
// otherwise. It reads PODIMO_BIND_HOST (the same env var the server uses),
// defaults to 127.0.0.1:12104, and normalizes wildcard bind hosts (0.0.0.0,
// ::, and empty host) to 127.0.0.1 so the probe connects via loopback.
// Side-effect-free: it does not load full config.
func runHealthcheck() int {
	addr := os.Getenv("PODIMO_BIND_HOST")
	if addr == "" {
		addr = "127.0.0.1:12104"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	searchQuery := r.URL.Query().Get("q")
	if len(searchQuery) < 2 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		a.templates.ExecuteTemplate(w, "search_results.html", map[string]any{
			"Error": "Query must be at least 2 characters",
		})
		return
	}

	username, password, region, locale, ok := a.resolveCredentials(r, w)
	if !ok {
		return
	}

	if !a.cfg.isValidRegion(region) {
		http.Error(w, "Invalid region", http.StatusBadRequest)
		return
	}
	if !a.cfg.isValidLocale(locale) {
		http.Error(w, "Invalid locale", http.StatusBadRequest)
		return
	}

	client, err := a.checkAuth(r.Context(), username, password, region, locale)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		a.templates.ExecuteTemplate(w, "search_results.html", map[string]any{
			"Error": "Authentication required",
		})
		return
	}

	results, err := client.SearchPodcasts(r.Context(), searchQuery)
	if err != nil {
		if _, ok := err.(*podimo.AuthenticationError); ok {
			if refreshErr := client.RefreshToken(r.Context()); refreshErr == nil {
				results, err = client.SearchPodcasts(r.Context(), searchQuery)
			}
		}
	}
	if err != nil {
		a.logger.Error("Search error", "error", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		a.templates.ExecuteTemplate(w, "search_results.html", map[string]any{
			"Error": "Search failed. Podimo may have changed their API.",
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	a.templates.ExecuteTemplate(w, "search_results.html", map[string]any{
		"Results": results,
		"Query":   searchQuery,
	})
}

func (a *App) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	username, password, region, locale, ok := a.resolveCredentials(r, w)
	if !ok {
		return
	}

	if !a.cfg.isValidRegion(region) {
		http.Error(w, "Invalid region", http.StatusBadRequest)
		return
	}
	if !a.cfg.isValidLocale(locale) {
		http.Error(w, "Invalid locale", http.StatusBadRequest)
		return
	}

	client, err := a.checkAuth(r.Context(), username, password, region, locale)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		a.templates.ExecuteTemplate(w, "subscriptions.html", map[string]any{
			"Error": "Authentication required",
		})
		return
	}

	results, err := client.GetFollowedPodcasts(r.Context())
	if err != nil {
		if _, ok := err.(*podimo.AuthenticationError); ok {
			if refreshErr := client.RefreshToken(r.Context()); refreshErr == nil {
				results, err = client.GetFollowedPodcasts(r.Context())
			}
		}
	}
	if err != nil {
		a.logger.Error("Subscriptions error", "error", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		a.templates.ExecuteTemplate(w, "subscriptions.html", map[string]any{
			"Error": "Failed to fetch subscriptions",
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	a.templates.ExecuteTemplate(w, "subscriptions.html", map[string]any{
		"Results": results,
	})
}

func (a *App) handleFeed(w http.ResponseWriter, r *http.Request) {
	podcastID := chi.URLParam(r, "podcast_id")

	if !podcastIDPattern.MatchString(podcastID) {
		http.Error(w, "Invalid podcast id format", http.StatusBadRequest)
		return
	}

	username, password, region, locale, ok := a.resolveCredentials(r, w)
	if !ok {
		return
	}

	a.serveFeed(w, r, podcastID, username, password, region, locale)
}

func (a *App) handleFeedPath(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	password := chi.URLParam(r, "password")
	podcastID := chi.URLParam(r, "podcast_id")

	username, _ = url.PathUnescape(username)
	password, _ = url.PathUnescape(password)

	region, locale := a.defaultRegionLocale(r)

	if !podcastIDPattern.MatchString(podcastID) {
		http.Error(w, "Invalid podcast id format", http.StatusBadRequest)
		return
	}

	a.serveFeed(w, r, podcastID, username, password, region, locale)
}

// resolveCredentials extracts username/password/region/locale for Basic-Auth and
// local-credentials modes. ok is false when the caller has already written a
// response (401 challenge) and must return immediately.
func (a *App) resolveCredentials(r *http.Request, w http.ResponseWriter) (username, password, region, locale string, ok bool) {
	if a.cfg.LocalCredentials {
		username = a.cfg.Email
		password = a.cfg.Password
		region, locale = a.defaultRegionLocale(r)
		return username, password, region, locale, true
	}
	user, pass, authOK := r.BasicAuth()
	if !authOK {
		authenticate(w)
		return "", "", "", "", false
	}
	username, region, locale = splitUsernameRegionLocale(user)
	password = pass
	return username, password, region, locale, true
}

// defaultRegionLocale returns the region/locale from query params, defaulting to
// Dutch for podcast-app compatibility.
func (a *App) defaultRegionLocale(r *http.Request) (region, locale string) {
	region = r.URL.Query().Get("region")
	if region == "" {
		region = "nl"
	}
	locale = r.URL.Query().Get("locale")
	if locale == "" {
		locale = "nl-NL"
	}
	return region, locale
}

// serveFeed handles the shared tail of handleFeed/handleFeedPath: block-list check,
// auth, episode fetch (with one stale-token refresh retry), and RSS generation.
func (a *App) serveFeed(w http.ResponseWriter, r *http.Request, podcastID, username, password, region, locale string) {
	if !a.cfg.isValidRegion(region) {
		http.Error(w, "Invalid region", http.StatusBadRequest)
		return
	}
	if !a.cfg.isValidLocale(locale) {
		http.Error(w, "Invalid locale", http.StatusBadRequest)
		return
	}

	if _, ok := a.cfg.Blocked[podcastID]; ok {
		http.Error(w, "Podcast is gone", http.StatusGone)
		return
	}

	client, err := a.checkAuth(r.Context(), username, password, region, locale)
	if err != nil {
		authenticate(w)
		return
	}

	data, err := client.GetPodcasts(r.Context(), podcastID, a.cfg.PodcastCacheTime)
	if err != nil {
		if _, ok := err.(*podimo.AuthenticationError); ok {
			if refreshErr := client.RefreshToken(r.Context()); refreshErr == nil {
				data, err = client.GetPodcasts(r.Context(), podcastID, a.cfg.PodcastCacheTime)
			}
		}
	}
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

	httpClient := a.getHTTPClient(client.Key())
	rssXML, err := podimo.PodcastsToRss(r.Context(), podcastID, data, locale, a.headCache, a.cfg.PublicFeeds, a.cfg.HeadCacheTime, httpClient, a.logger)
	if err != nil {
		a.logger.Error("RSS generation error", "error", err)
		http.Error(w, "Something went wrong while generating the RSS feed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Write(rssXML)
}

func (a *App) graphqlEndpoint() string {
	if a.cfg.ScraperAPI != "" {
		return fmt.Sprintf("https://api.scraperapi.com?api_key=%s&url=%s&keep_headers=true", a.cfg.ScraperAPI, url.QueryEscape(a.cfg.GraphQLURL))
	}
	return a.cfg.GraphQLURL
}

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

func (a *App) checkAuth(ctx context.Context, username, password, region, locale string) (*podimo.PodimoClient, error) {
	key := podimo.TokenKey(username, password)
	httpClient := a.getHTTPClient(key)
	graphql := podimo.NewGraphQLClient(a.graphqlEndpoint(), httpClient)
	var tokenCache *podimo.FileCache
	if a.cfg.StoreTokensOnDisk {
		tokenCache = a.tokenCache
	}
	client, err := podimo.NewPodimoClient(username, password, region, locale, graphql, tokenCache, a.podcastCache, a.logger)
	if err != nil {
		return nil, err
	}
	if client.Token() != "" {
		return client, nil
	}
	token, err := client.Login(ctx)
	if err != nil {
		return nil, err
	}
	if token != "" && a.cfg.StoreTokensOnDisk {
		if err := a.tokenCache.Set(key, token, a.cfg.TokenCacheTime); err != nil {
			a.logger.Error("token cache write", "error", err)
		}
	}
	return client, nil
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			a.renderIndex(w, "Invalid form data", "")
			return
		}

		podcastID := r.FormValue("podcast_id")
		if podcastID == "" {
			a.renderIndex(w, "Podcast ID is required", "")
			return
		}

		if !podcastIDPattern.MatchString(podcastID) {
			match := urlUUIDPattern.FindString(podcastID)
			if match == "" {
				a.renderIndex(w, "Podcast ID is not valid", "")
				return
			}
			podcastID = match
		}

		region := r.FormValue("region")
		if !a.cfg.isValidRegion(region) {
			a.renderIndex(w, "Region is not valid", "")
			return
		}

		locale := r.FormValue("locale")
		if !a.cfg.isValidLocale(locale) {
			a.renderIndex(w, "Locale is not valid", "")
			return
		}

		var feedURL string
		if a.cfg.LocalCredentials {
			feedURL = a.buildFeedURL(podcastID, region, locale)
		} else {
			email := r.FormValue("email")
			password := r.FormValue("password")
			userPart := fmt.Sprintf("%s,%s,%s", email, region, locale)
			feedURL = fmt.Sprintf("%s://%s@%s/feed/%s.xml?random=%s",
				a.cfg.Protocol, url.UserPassword(userPart, password).String(), a.cfg.Hostname, podcastID, podimo.RandomHexID(8))
		}
		a.renderFeedLocation(w, feedURL)
		return
	}

	data := map[string]any{
		"Regions":         a.cfg.Regions,
		"Locales":         a.cfg.Locales,
		"NeedCredentials": !a.cfg.LocalCredentials,
	}
	if err := a.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		a.logger.Error("Template render error", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (a *App) renderIndex(w http.ResponseWriter, errorMsg, podcastID string) {
	data := map[string]any{
		"Error":           errorMsg,
		"Regions":         a.cfg.Regions,
		"Locales":         a.cfg.Locales,
		"PodcastID":       podcastID,
		"NeedCredentials": !a.cfg.LocalCredentials,
	}
	if err := a.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		a.logger.Error("Template render error", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (a *App) renderFeedLocation(w http.ResponseWriter, feedURL string) {
	data := map[string]any{
		"URL": feedURL,
	}
	if err := a.templates.ExecuteTemplate(w, "feed_location.html", data); err != nil {
		a.logger.Error("Template render error", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (a *App) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(exampleText()))
}

func (a *App) buildFeedURL(podcastID, region, locale string) string {
	return fmt.Sprintf("%s://%s/feed/%s.xml?region=%s&locale=%s&random=%s",
		a.cfg.Protocol, a.cfg.Hostname, podcastID, region, locale, podimo.RandomHexID(8))
}

func splitUsernameRegionLocale(user string) (username, region, locale string) {
	parts := strings.Split(user, ",")
	if len(parts) != 3 {
		return "", "nl", "nl-NL"
	}
	return parts[0], parts[1], parts[2]
}

func exampleText() string {
	return `404 Not found.

How to use: Visit the main page and enter a podcast ID to receive an RSS URL.

Example
-------------
Username: example@example.com
Password: this-is-my-password
Podcast ID: 12345-abcdef

The URL will be
https://example%40example.com:this-is-my-password@localhost:12104/feed/12345-abcdef.xml

Note that the username and password should be URL encoded. This can be done with
a tool like https://gchq.github.io/CyberChef/#recipe=URL_Encode(true)
`
}

func authenticate(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Podimo-to-RSS", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
