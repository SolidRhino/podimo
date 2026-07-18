package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolidRhino/podimo-rss/podimo"
)

func setupTestApp(t *testing.T) *App {
	dir := t.TempDir()
	tokenCache, _ := podimo.NewFileCache(dir + "/tokens")
	podcastCache, _ := podimo.NewFileCache(dir + "/podcasts")
	headCache, _ := podimo.NewFileCache(dir + "/head")

	cfg := &Config{
		Hostname:          "localhost:12104",
		BindHost:          "127.0.0.1:12104",
		Protocol:          "http",
		CacheDir:          dir,
		Locales:           []string{"nl-NL", "en-US"},
		Regions:           []Region{{Code: "nl", Name: "Nederland"}, {Code: "en", Name: "International"}},
		Blocked:           make(map[string]struct{}),
		TokenCacheTime:    time.Hour,
		PodcastCacheTime:  time.Hour,
		HeadCacheTime:     time.Hour,
		StoreTokensOnDisk: true,
		DateFormat:        "2006-01-02",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"formatDate": func(raw string) string {
			if raw == "" {
				return ""
			}
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				return t.Format(cfg.DateFormat)
			}
			return raw
		},
	}).ParseFS(templatesFS, "templates/index.html", "templates/feed_location.html", "templates/partials/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}

	app := &App{
		cfg:          cfg,
		logger:       logger,
		limiter:      NewRateLimiter(10*time.Second, 8),
		tokenCache:   tokenCache,
		podcastCache: podcastCache,
		headCache:    headCache,
		clients: podimo.NewBoundedMap[string, *http.Client](podimo.BoundedMapOptions{
			MaxSize: 100,
			TTL:     time.Hour,
		}),
		templates: tmpl,
	}
	t.Cleanup(func() {
		app.limiter.ips.Stop()
		app.clients.Stop()
	})
	return app
}

func TestHealthHandler(t *testing.T) {
	app := setupTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	app.handleHealth(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Fatalf("expected ok status, got %s", rr.Body.String())
	}
}

func TestRunHealthcheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("PODIMO_BIND_HOST", addr)
	if code := runHealthcheck(); code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestRunHealthcheck_Unreachable(t *testing.T) {
	t.Setenv("PODIMO_BIND_HOST", "127.0.0.1:1")
	if code := runHealthcheck(); code != 1 {
		t.Fatalf("expected exit 1 for unreachable, got %d", code)
	}
}

func TestRunHealthcheck_ZeroZeroZeroZeroNormalized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	addr = strings.Replace(addr, "127.0.0.1", "0.0.0.0", 1)
	t.Setenv("PODIMO_BIND_HOST", addr)
	if code := runHealthcheck(); code != 0 {
		t.Fatalf("expected exit 0 with 0.0.0.0 normalization, got %d", code)
	}
}

func TestRunHealthcheck_EmptyHostNormalized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	addr = ":" + strings.SplitN(addr, ":", 2)[1]
	t.Setenv("PODIMO_BIND_HOST", addr)
	if code := runHealthcheck(); code != 0 {
		t.Fatalf("expected exit 0 with empty host normalization, got %d", code)
	}
}

func TestIndexHandler(t *testing.T) {
	app := setupTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	app.handleIndex(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Podimo-to-RSS converter") {
		t.Fatalf("expected form title")
	}
	if !strings.Contains(body, `name="q"`) {
		t.Errorf("expected search input name=\"q\", got body:\n%s", body)
	}
	if !strings.Contains(body, `hx-include="[name='region'], [name='locale']"`) {
		t.Errorf("expected region/locale wired into hx-include, got body:\n%s", body)
	}
	if !strings.Contains(body, "email.value + ',' + regionVal + ',' + localeVal") {
		t.Errorf("expected Basic-auth username to carry region/locale in JS, got body:\n%s", body)
	}
}

func TestNotFoundHandler(t *testing.T) {
	app := setupTestApp(t)
	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain, got %s", ct)
	}
}

func TestRateLimiting(t *testing.T) {
	app := setupTestApp(t)
	router := app.setupRoutes()
	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if i == 8 && rr.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on 9th request, got %d", rr.Code)
		}
	}
}

func setupTestAppWithMock(t *testing.T, mockURL string) *App {
	app := setupTestApp(t)
	app.cfg.GraphQLURL = mockURL
	return app
}

func TestHandleFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcast": map[string]interface{}{
					"title":       "Test Podcast",
					"description": "Desc",
					"authorName":  "Author",
					"language":    "en",
					"images": map[string]interface{}{
						"coverImageUrl": "http://cover.jpg",
					},
				},
				"episodes": []interface{}{
					map[string]interface{}{
						"id":              "ep1",
						"title":           "Episode 1",
						"description":     "Desc 1",
						"publishDatetime": "2023-01-01T00:00:00Z",
						"audio": map[string]interface{}{
							"url":      "http://audio.mp3",
							"duration": 60.0,
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"

	// Pre-cache token and head info to skip login and HEAD requests
	key := podimo.TokenKey("u", "p")
	_ = app.tokenCache.Set(key, "fake-token", time.Hour)
	_ = app.headCache.Set("ep1", map[string]interface{}{"length": "100", "type": "audio/mpeg"}, time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/12345678-1234-1234-1234-123456789abc.xml?region=nl&locale=nl-NL", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<title>Test Podcast</title>") {
		t.Fatalf("expected podcast title in RSS, got %s", body)
	}
	if !strings.Contains(body, "<enclosure") {
		t.Fatalf("expected enclosure in RSS")
	}
}

func TestHandleFeedPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcast": map[string]interface{}{
					"title":       "Path Podcast",
					"description": "Desc",
					"authorName":  "Author",
					"language":    "en",
					"images": map[string]interface{}{
						"coverImageUrl": "http://cover.jpg",
					},
				},
				"episodes": []interface{}{
					map[string]interface{}{
						"id":              "ep2",
						"title":           "Episode 2",
						"description":     "Desc 2",
						"publishDatetime": "2023-02-01T00:00:00Z",
						"audio": map[string]interface{}{
							"url":      "http://audio2.mp3",
							"duration": 120.0,
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	// URL-embedded credentials mode
	key := podimo.TokenKey("user", "pass")
	_ = app.tokenCache.Set(key, "fake-token", time.Hour)
	_ = app.headCache.Set("ep2", map[string]interface{}{"length": "200", "type": "audio/mpeg"}, time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/user/pass/12345678-1234-1234-1234-123456789abc.xml?region=nl&locale=nl-NL", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<title>Path Podcast</title>") {
		t.Fatalf("expected podcast title in RSS, got %s", body)
	}
}

func TestHandleFeed_InvalidUUID(t *testing.T) {
	app := setupTestApp(t)
	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/not-a-uuid.xml", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleFeed_Blocked(t *testing.T) {
	app := setupTestApp(t)
	app.cfg.Blocked["12345678-1234-1234-1234-123456789abc"] = struct{}{}
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/12345678-1234-1234-1234-123456789abc.xml", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", rr.Code)
	}
}

func TestHandleSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsAutocomplete": []interface{}{
					map[string]interface{}{
						"id":    "p1",
						"title": "Podcast 1",
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	_ = app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Podcast 1") {
		t.Fatalf("expected HTML containing 'Podcast 1', got: %s", body)
	}
}

func TestHandleSubscriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsFollowed": []interface{}{
					map[string]interface{}{
						"id":    "p2",
						"title": "Followed Podcast",
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	_ = app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/subscriptions", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Followed Podcast") {
		t.Fatalf("expected HTML containing 'Followed Podcast', got: %s", body)
	}
}

func TestHandleSubscriptions_ShowsMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsFollowed": []interface{}{
					map[string]interface{}{
						"id":            "p1",
						"title":         "Metadata Show",
						"coverImageUrl": "http://cover.jpg",
						"episodeCount":  12,
						"latestEpisode": map[string]interface{}{
							"publishDatetime": "2024-05-01T00:00:00Z",
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	_ = app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/subscriptions", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "12 episodes") {
		t.Fatalf("expected '12 episodes' in HTML, got: %s", body)
	}
	if !strings.Contains(body, "updated 2024-05-01") {
		t.Fatalf("expected 'updated 2024-05-01' in HTML, got: %s", body)
	}
}

func TestHandleFeed_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "Unauthorized"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	key := podimo.TokenKey("u", "p")
	_ = app.tokenCache.Set(key, "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/12345678-1234-1234-1234-123456789abc.xml?region=nl&locale=nl-NL", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFeedPath_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "unauthenticated"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	key := podimo.TokenKey("user", "pass")
	_ = app.tokenCache.Set(key, "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/user/pass/12345678-1234-1234-1234-123456789abc.xml?region=nl&locale=nl-NL", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestLoggingMiddleware_Redaction(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/feed/u/secret/12345678-1234-1234-1234-123456789abc.xml", "/feed/REDACTED/REDACTED/12345678-1234-1234-1234-123456789abc.xml"},
		{"/feed/u/secret/12345678-1234-1234-1234-123456789abc.xml?region=nl", "/feed/REDACTED/REDACTED/12345678-1234-1234-1234-123456789abc.xml?region=nl"},
		{"/feed/12345678-1234-1234-1234-123456789abc.xml", "/feed/12345678-1234-1234-1234-123456789abc.xml"},
		{"/search?q=test", "/search?q=test"},
		{"/health", "/health"},
	}

	for _, c := range cases {
		got := redactURLString(c.input)
		if got != c.expected {
			t.Fatalf("redactURLString(%q) = %q; want %q", c.input, got, c.expected)
		}
	}
}

func TestHandleIndexPost(t *testing.T) {
	app := setupTestApp(t)
	app.cfg.LocalCredentials = true

	router := app.setupRoutes()
	form := url.Values{}
	form.Set("podcast_id", "12345678-1234-1234-1234-123456789abc")
	form.Set("region", "nl")
	form.Set("locale", "nl-NL")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "12345678-1234-1234-1234-123456789abc") {
		t.Fatalf("expected podcast ID in feed URL, got %s", body)
	}
	if !strings.Contains(body, "/feed/") {
		t.Fatalf("expected feed URL in response, got %s", body)
	}
}

func TestCheckAuth_StoreTokensOnDiskFalse(t *testing.T) {
	srv := mockGraphQLServer(t, []map[string]interface{}{
		{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}},
		{"userOnboardingFlow": map[string]interface{}{"id": "oid"}},
		{"tokenWithCredentials": map[string]interface{}{"token": "final"}},
	})
	t.Cleanup(srv.Close)

	app := setupTestApp(t)
	app.cfg.StoreTokensOnDisk = false
	app.cfg.GraphQLURL = srv.URL

	key := podimo.TokenKey("u", "p")
	if _, ok := app.tokenCache.Get(key); ok {
		t.Fatal("tokenCache should be empty before checkAuth")
	}

	_, err := app.checkAuth(context.Background(), "u", "p", "nl", "nl-NL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := app.tokenCache.Get(key); ok {
		t.Fatal("expected cache to stay empty when StoreTokensOnDisk=false")
	}
}

// Helper that matches the client_test pattern but lives in main_test
func mockGraphQLServer(t *testing.T, responses []map[string]interface{}) *httptest.Server {
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if idx < len(responses) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": responses[idx],
			})
			idx++
		} else {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{},
			})
		}
	}))
}

func TestLoadConfig_InvalidBool(t *testing.T) {
	t.Setenv("PODIMO_DEBUG", "maybe")
	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected error for invalid DEBUG value")
	}
	if !strings.Contains(err.Error(), "debug") {
		t.Fatalf("expected debug in error, got %v", err)
	}
}

func TestLoadConfig_InvalidDuration(t *testing.T) {
	t.Setenv("PODIMO_TOKEN_CACHE_TIME", "forever")
	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected error for invalid TOKEN_CACHE_TIME value")
	}
	if !strings.Contains(err.Error(), "token_cache_time") {
		t.Fatalf("expected token_cache_time in error, got %v", err)
	}
}

func TestLoadConfig_TrimmedBool(t *testing.T) {
	t.Setenv("PODIMO_DEBUG", " true ")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Debug {
		t.Fatal("expected true after trimming")
	}
}

func TestLoadConfig_TrimmedDuration(t *testing.T) {
	t.Setenv("PODIMO_TOKEN_CACHE_TIME", " 3600 ")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TokenCacheTime != 3600*time.Second {
		t.Fatalf("expected 3600s, got %v", cfg.TokenCacheTime)
	}
}

func TestLoadConfig_DateFormatDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, key := range []string{"hostname", "bind_host", "protocol", "debug", "date_format"} {
		t.Setenv("PODIMO_"+strings.ToUpper(key), "")
	}
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DateFormat != "2006-01-02" {
		t.Fatalf("expected default date_format 2006-01-02, got %q", cfg.DateFormat)
	}
}

func TestLoadConfig_DateFormatOverride(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, key := range []string{"hostname", "bind_host", "protocol", "debug", "email", "password"} {
		t.Setenv("PODIMO_"+strings.ToUpper(key), "")
	}
	t.Setenv("PODIMO_DATE_FORMAT", "Jan 2, 2006")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DateFormat != "Jan 2, 2006" {
		t.Fatalf("expected Jan 2, 2006, got %q", cfg.DateFormat)
	}
}

func TestHandleSubscriptions_FormatsDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsFollowed": []interface{}{
					map[string]interface{}{
						"id":            "p1",
						"title":         "Date Format Show",
						"coverImageUrl": "http://cover.jpg",
						"episodeCount":  42,
						"latestEpisode": map[string]interface{}{
							"publishDatetime": "2024-05-01T00:00:00Z",
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	// Override the default format to a locale-style layout.
	app.cfg.DateFormat = "Jan 2, 2006"
	// Re-parse templates with the new format's FuncMap.
	app.templates = template.Must(template.New("").Funcs(template.FuncMap{
		"formatDate": func(raw string) string {
			if raw == "" {
				return ""
			}
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				return t.Format(app.cfg.DateFormat)
			}
			return raw
		},
	}).ParseFS(templatesFS, "templates/index.html", "templates/feed_location.html", "templates/partials/*.html"))
	_ = app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/subscriptions", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "updated May 1, 2024") {
		t.Fatalf("expected 'updated May 1, 2024' in HTML, got: %s", body)
	}
}

func TestLoadConfig_WithYAMLFile(t *testing.T) {
	// Run from a temp dir so godotenv.Load(".env") inside LoadConfig finds no
	// .env file, and blank any PODIMO_* vars leaked into the process env by
	// earlier tests/godotenv so the koanf env provider's empty-skip transform
	// drops them and falls back to the YAML file values instead of the repo .env.
	t.Chdir(t.TempDir())
	for _, key := range []string{"hostname", "bind_host", "protocol", "debug", "local_credentials", "email", "password", "podcast_cache_time", "public_feeds", "token_cache_time", "head_cache_time", "http_proxy", "zenrows_api", "scraper_api"} {
		t.Setenv("PODIMO_"+strings.ToUpper(key), "")
	}
	content := `hostname: "podimo.example.com"
bind_host: "0.0.0.0:3000"
protocol: "https"
debug: true
local_credentials: true
email: "alice@example.com"
password: "secret"
token_cache_time: "120h"
podcast_cache_time: "3600s"
head_cache_time: "604800s"
public_feeds: true
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(%q): %v", path, err)
	}
	if cfg.Hostname != "podimo.example.com" {
		t.Errorf("hostname: got %q, want %q", cfg.Hostname, "podimo.example.com")
	}
	if cfg.BindHost != "0.0.0.0:3000" {
		t.Errorf("bind_host: got %q, want %q", cfg.BindHost, "0.0.0.0:3000")
	}
	if cfg.Protocol != "https" {
		t.Errorf("protocol: got %q, want %q", cfg.Protocol, "https")
	}
	if !cfg.Debug {
		t.Error("debug: expected true")
	}
	if !cfg.LocalCredentials {
		t.Error("local_credentials: expected true")
	}
	if cfg.Email != "alice@example.com" {
		t.Errorf("email: got %q, want %q", cfg.Email, "alice@example.com")
	}
	if cfg.Password != "secret" {
		t.Errorf("password: got %q, want %q", cfg.Password, "secret")
	}
	if cfg.TokenCacheTime != 120*time.Hour {
		t.Errorf("token_cache_time: got %v, want %v", cfg.TokenCacheTime, 120*time.Hour)
	}
	if cfg.PodcastCacheTime != 3600*time.Second {
		t.Errorf("podcast_cache_time: got %v, want %v", cfg.PodcastCacheTime, 3600*time.Second)
	}
	if cfg.HeadCacheTime != 604800*time.Second {
		t.Errorf("head_cache_time: got %v, want %v", cfg.HeadCacheTime, 604800*time.Second)
	}
	if !cfg.PublicFeeds {
		t.Error("public_feeds: expected true")
	}
}

// TestLoadConfig_EnvOnlyCredentials guards that env-only credentials
// (email/password with no file or default entry) still populate the struct.
// Under koanf the env provider emits all PODIMO_* keys directly — no BindEnv
// registry is needed — so this remains a meaningful regression guard for
// local_credentials mode seeing populated email/password.
func TestLoadConfig_EnvOnlyCredentials(t *testing.T) {
	t.Setenv("PODIMO_EMAIL", "envonly@example.com")
	t.Setenv("PODIMO_PASSWORD", "envsecret")
	t.Setenv("PODIMO_LOCAL_CREDENTIALS", "true")
	t.Setenv("PODIMO_HTTP_PROXY", "http://proxy:8080")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig(\"\"): %v", err)
	}
	if !cfg.LocalCredentials {
		t.Error("local_credentials: expected true")
	}
	if cfg.Email != "envonly@example.com" {
		t.Errorf("email: got %q, want %q", cfg.Email, "envonly@example.com")
	}
	if cfg.Password != "envsecret" {
		t.Errorf("password: got %q, want %q", cfg.Password, "envsecret")
	}
	if cfg.HTTPProxy != "http://proxy:8080" {
		t.Errorf("http_proxy: got %q, want %q", cfg.HTTPProxy, "http://proxy:8080")
	}
}

func TestRateLimiter_SlidingWindow(t *testing.T) {
	rl := NewRateLimiter(50*time.Millisecond, 2)
	if !rl.Allow("1.2.3.4") {
		t.Fatal("first call should be allowed")
	}
	if !rl.Allow("1.2.3.4") {
		t.Fatal("second call should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("third call in same window should be denied")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow("1.2.3.4") {
		t.Fatal("call after window elapsed should be allowed")
	}
}

func TestRateLimitMiddleware_TrustedProxyHeader(t *testing.T) {
	app := setupTestApp(t)
	app.cfg.TrustedProxyHeader = "X-Forwarded-For"
	app.limiter = NewRateLimiter(10*time.Second, 1) // max=1 per IP
	router := app.setupRoutes()

	// Both requests share the same RemoteAddr (httptest default) but have
	// different X-Forwarded-For values. If the header is used, each "client"
	// gets its own bucket and both pass. If RemoteAddr were used, the second
	// would be rate-limited (max=1).
	for _, xff := range []string{"1.1.1.1", "2.2.2.2"} {
		req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
		req.Header.Set("X-Forwarded-For", xff)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("request with X-Forwarded-For %s should not be rate-limited (distinct IP), got %d", xff, rr.Code)
		}
	}

	// A second request from the same XFF IP should now be rate-limited (max=1).
	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limiting for same XFF IP, got %d", rr.Code)
	}
}

func TestHandleFeed_StaleTokenRetry(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)

		// Call 1: GetPodcasts with stale token → auth error
		if callCount == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []map[string]interface{}{
					{"message": "Unauthorized"},
				},
			})
			return
		}
		// Calls 2-4: RefreshToken → 3-step login (preregister, onboarding, authorize)
		if callCount == 2 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}}})
			return
		}
		if callCount == 3 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"userOnboardingFlow": map[string]interface{}{"id": "oid"}}})
			return
		}
		if callCount == 4 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"tokenWithCredentials": map[string]interface{}{"token": "fresh-token"}}})
			return
		}
		// Call 5: GetPodcasts with fresh token → success
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcast": map[string]interface{}{
					"title":       "Retry Podcast",
					"description": "Desc",
					"authorName":  "Author",
					"language":    "en",
					"images":      map[string]interface{}{"coverImageUrl": "http://cover.jpg"},
				},
				"episodes": []interface{}{
					map[string]interface{}{
						"id":              "ep1",
						"title":           "Episode 1",
						"description":     "Desc 1",
						"publishDatetime": "2023-01-01T00:00:00Z",
						"audio":           map[string]interface{}{"url": "http://audio.mp3", "duration": 60.0},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	key := podimo.TokenKey("u", "p")
	_ = app.tokenCache.Set(key, "stale-token", time.Hour)
	_ = app.headCache.Set("ep1", map[string]interface{}{"length": "100", "type": "audio/mpeg"}, time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/feed/12345678-1234-1234-1234-123456789abc.xml?region=nl&locale=nl-NL", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after stale-token retry, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<title>Retry Podcast</title>") {
		t.Fatalf("expected RSS with Retry Podcast, got %s", rr.Body.String())
	}
	// Stale token should have been deleted from cache.
	if _, ok := app.tokenCache.Get(key); ok {
		t.Fatal("expected stale token to be deleted from cache")
	}
}

type trackingTransport struct {
	closedIdle bool
	base       http.RoundTripper
}

func (t *trackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req)
}

func (t *trackingTransport) CloseIdleConnections() {
	t.closedIdle = true
	if t.base != nil {
		if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
			c.CloseIdleConnections()
		}
	}
}

func TestClientsMap_EvictionClosesIdleConnections(t *testing.T) {
	// Build a clients BoundedMap with MaxSize=1 and the same OnEvict callback
	// used in main() so eviction calls CloseIdleConnections on the evicted client.
	clients := podimo.NewBoundedMap[string, *http.Client](podimo.BoundedMapOptions{
		MaxSize: 1,
		OnEvict: func(v any) {
			if c, ok := v.(*http.Client); ok {
				c.CloseIdleConnections()
			}
		},
	})

	tt1 := &trackingTransport{base: http.DefaultTransport}
	c1 := &http.Client{Transport: tt1}
	tt2 := &trackingTransport{base: http.DefaultTransport}
	c2 := &http.Client{Transport: tt2}

	clients.Set("user1", c1, time.Hour)
	clients.Set("user2", c2, time.Hour) // evicts user1

	if !tt1.closedIdle {
		t.Fatal("expected evicted client's CloseIdleConnections to be called")
	}
	if tt2.closedIdle {
		t.Fatal("expected non-evicted client's CloseIdleConnections to NOT be called")
	}
}

func TestLoggingMiddleware_Status(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	app := setupTestApp(t)
	app.logger = logger

	// Build a handler that deliberately writes 404, wrapped in the middleware.
	mw := app.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/some-missing", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "status=404") {
		t.Fatalf("expected log to contain status=404, got:\n%s", logged)
	}
	if !strings.Contains(logged, "Request completed") {
		t.Fatalf("expected completion log line, got:\n%s", logged)
	}
}

func TestHandleReady_Reachable(t *testing.T) {
	// Any HTTP response (even 405) proves reachability.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)
	app := setupTestApp(t)
	app.ready = &readyProbe{endpoint: srv.URL}

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	app.handleReady(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 ready, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"ready"`) {
		t.Fatalf("expected ready body, got %s", rr.Body.String())
	}
}

func TestHandleReady_Unreachable(t *testing.T) {
	app := setupTestApp(t)
	// Port 1 is reserved and will refuse connections.
	app.ready = &readyProbe{endpoint: "http://127.0.0.1:1"}

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	app.handleReady(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"not ready"`) {
		t.Fatalf("expected not-ready body, got %s", rr.Body.String())
	}
}

func TestHandleReady_Cached(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		atomic.AddInt32(&hits, 1)
	}))
	t.Cleanup(srv.Close)
	app := setupTestApp(t)
	app.ready = &readyProbe{endpoint: srv.URL}

	// First call probes the server.
	req1 := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr1 := httptest.NewRecorder()
	app.handleReady(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first call: expected 200, got %d", rr1.Code)
	}

	// Second call within the 10s cache window must NOT hit the server again.
	req2 := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr2 := httptest.NewRecorder()
	app.handleReady(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second call: expected 200, got %d", rr2.Code)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected server to be hit exactly once, got %d", got)
	}
}

func TestWithAuthRetry_RefreshSuccess(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		switch callCount {
		case 1:
			// First GetPodcasts attempt: auth error (stale token).
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []map[string]interface{}{{"message": "Unauthorized"}},
			})
		case 2, 3, 4:
			// RefreshToken: 3-step login handshake.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"},
					"userOnboardingFlow":       map[string]interface{}{"id": "oid"},
					"tokenWithCredentials":     map[string]interface{}{"token": "fresh-token"},
				},
			})
		default:
			// Retry GetPodcasts after refresh: success.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"podcast":  map[string]interface{}{"title": "T"},
					"episodes": []interface{}{},
				},
			})
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tc, _ := podimo.NewFileCache(dir + "/tokens")
	pc, _ := podimo.NewFileCache(dir + "/podcasts")
	// Set the stale token BEFORE constructing the client so the constructor
	// loads it into client.token and GetPodcasts uses it (and hits the auth
	// error) on the first attempt.
	_ = tc.Set(podimo.TokenKey("u", "p"), "stale-token", time.Hour)
	gl := podimo.NewGraphQLClient(srv.URL, srv.Client())
	client, _ := podimo.NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)

	ctx := context.Background()
	data, err := withAuthRetry(ctx, client, func(c context.Context) (*podimo.PodcastData, error) {
		return client.GetPodcasts(c, "pid", 0)
	})
	if err != nil {
		t.Fatalf("expected success after refresh, got %v", err)
	}
	if data == nil || data.Podcast.Title != "T" {
		t.Fatalf("expected data with title T, got %+v", data)
	}
}

func TestWithAuthRetry_NoAuthError(t *testing.T) {
	// A non-auth error must be returned as-is, with no refresh attempted.
	// withAuthRetry only calls RefreshToken on *AuthenticationError, so a
	// generic error passes through unchanged even with a nil client.
	got, err := withAuthRetry(context.Background(), nil, func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("network down")
	})
	if err == nil || err.Error() != "network down" {
		t.Fatalf("expected 'network down' error to pass through, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected zero value, got %q", got)
	}
}

func TestWithAuthRetry_SuccessNoRetry(t *testing.T) {
	// fn succeeds on first call — no refresh, no second invocation.
	var calls int
	got, err := withAuthRetry(context.Background(), nil, func(ctx context.Context) (int, error) {
		calls++
		return 42, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
	if calls != 1 {
		t.Fatalf("expected fn called exactly once, got %d", calls)
	}
}

// feedMockServer returns a mock Podimo GraphQL server that serves a single
// episode with a fixed publish date. Reused by the feed-caching tests.
func feedMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcast": map[string]interface{}{
					"title":       "Cache Podcast",
					"description": "Desc",
					"authorName":  "Author",
					"language":    "en",
					"images":      map[string]interface{}{"coverImageUrl": "http://cover.jpg"},
				},
				"episodes": []interface{}{
					map[string]interface{}{
						"id":              "ep1",
						"title":           "Episode 1",
						"publishDatetime": "2023-01-01T00:00:00Z",
						"audio":           map[string]interface{}{"url": "http://audio.mp3", "duration": 60.0},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// feedTestApp builds a test app pointed at a feed mock with token/head caches
// pre-populated so requests skip login and HEAD requests.
func feedTestApp(t *testing.T, srv *httptest.Server) *App {
	t.Helper()
	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	_ = app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)
	_ = app.headCache.Set("ep1", map[string]interface{}{"length": "100", "type": "audio/mpeg"}, time.Hour)
	return app
}

const feedTestPath = "/feed/12345678-1234-1234-1234-123456789abc.xml?region=nl&locale=nl-NL"

func TestHandleFeed_ETag_304(t *testing.T) {
	srv := feedMockServer(t)
	app := feedTestApp(t, srv)
	router := app.setupRoutes()

	// First request captures the ETag.
	req1 := httptest.NewRequest(http.MethodGet, feedTestPath, nil)
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr1.Code, rr1.Body.String())
	}
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected non-empty ETag header")
	}

	// Second request with If-None-Match must return 304 and empty body.
	req2 := httptest.NewRequest(http.MethodGet, feedTestPath, nil)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if rr2.Body.Len() != 0 {
		t.Fatalf("expected empty body on 304, got %q", rr2.Body.String())
	}
}

func TestHandleFeed_CacheControl(t *testing.T) {
	srv := feedMockServer(t)
	app := feedTestApp(t, srv)
	router := app.setupRoutes()

	req := httptest.NewRequest(http.MethodGet, feedTestPath, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	cc := rr.Header().Get("Cache-Control")
	if !strings.Contains(cc, "max-age=3600") {
		t.Fatalf("expected Cache-Control with max-age=3600, got %q", cc)
	}
	if !strings.Contains(cc, "stale-while-revalidate") {
		t.Fatalf("expected stale-while-revalidate in Cache-Control, got %q", cc)
	}
}

func TestHandleFeed_LastModified_304(t *testing.T) {
	srv := feedMockServer(t)
	app := feedTestApp(t, srv)
	router := app.setupRoutes()

	// First request captures Last-Modified.
	req1 := httptest.NewRequest(http.MethodGet, feedTestPath, nil)
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr1.Code, rr1.Body.String())
	}
	lm := rr1.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("expected non-empty Last-Modified header")
	}

	// Second request with If-Modified-Since set to the same value must 304.
	req2 := httptest.NewRequest(http.MethodGet, feedTestPath, nil)
	req2.Header.Set("If-Modified-Since", lm)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if rr2.Body.Len() != 0 {
		t.Fatalf("expected empty body on 304, got %q", rr2.Body.String())
	}
}

func TestHandleSubscriptionsOPML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsFollowed": []interface{}{
					map[string]interface{}{"id": "id-a", "title": "Podcast A"},
					map[string]interface{}{"id": "id-b", "title": "Podcast B"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	_ = app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/subscriptions.opml?region=nl&locale=nl-NL", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/x-opml") {
		t.Fatalf("expected Content-Type text/x-opml, got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<opml") {
		t.Fatalf("expected <opml> root, got %s", body)
	}
	if !strings.Contains(body, "<outline") {
		t.Fatalf("expected <outline> entries, got %s", body)
	}
	if !strings.Contains(body, "Podcast A") || !strings.Contains(body, "Podcast B") {
		t.Fatalf("expected both podcast titles in OPML, got %s", body)
	}
	// Each outline must carry an xmlUrl that contains the podcast ID.
	if !strings.Contains(body, "id-a") || !strings.Contains(body, "id-b") {
		t.Fatalf("expected feed URLs containing podcast IDs in OPML, got %s", body)
	}
}
