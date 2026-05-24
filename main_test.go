package main

import (
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
		Hostname:         "localhost:12104",
		BindHost:         "127.0.0.1:12104",
		Protocol:         "http",
		CacheDir:         dir,
		Locales:          []string{"nl-NL", "en-US"},
		Regions:          []Region{{Code: "nl", Name: "Nederland"}, {Code: "en", Name: "International"}},
		Blocked:          make(map[string]struct{}),
		TokenCacheTime:   time.Hour,
		PodcastCacheTime: time.Hour,
		HeadCacheTime:    time.Hour,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	indexTmpl, err := template.ParseFS(templatesFS, "templates/index.html")
	if err != nil {
		t.Fatalf("parse index template: %v", err)
	}
	feedTmpl, err := template.ParseFS(templatesFS, "templates/feed_location.html")
	if err != nil {
		t.Fatalf("parse feed template: %v", err)
	}

	return &App{
		cfg:          cfg,
		logger:       logger,
		limiter:      NewRateLimiter(10*time.Second, 8),
		tokenCache:   tokenCache,
		podcastCache: podcastCache,
		headCache:    headCache,
		clients:      make(map[string]*http.Client),
		indexTmpl:    indexTmpl,
		feedTmpl:     feedTmpl,
	}
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

func TestIndexHandler(t *testing.T) {
	app := setupTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	app.handleIndex(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Podimo-to-RSS converter") {
		t.Fatalf("expected form title")
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
