package main

import (
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func setupTestAppWithMock(t *testing.T, mockURL string) *App {
	app := setupTestApp(t)
	app.cfg.GraphQLURL = mockURL
	return app
}

func TestHandleFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
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
	defer srv.Close()

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"

	// Pre-cache token and head info to skip login and HEAD requests
	key := podimo.TokenKey("u", "p")
	app.tokenCache.Set(key, "fake-token", time.Hour)
	app.headCache.Set("ep1", map[string]interface{}{"length": "100", "type": "audio/mpeg"}, time.Hour)

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
		json.NewEncoder(w).Encode(map[string]interface{}{
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
	defer srv.Close()

	app := setupTestAppWithMock(t, srv.URL)
	// URL-embedded credentials mode
	key := podimo.TokenKey("user", "pass")
	app.tokenCache.Set(key, "fake-token", time.Hour)
	app.headCache.Set("ep2", map[string]interface{}{"length": "200", "type": "audio/mpeg"}, time.Hour)

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

func TestHandleFeed_InvalidUUID(t*testing.T) {
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
		json.NewEncoder(w).Encode(map[string]interface{}{
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
	defer srv.Close()

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	results, ok := resp["results"].([]interface{})
	if !ok || len(results) != 1 {
		t.Fatalf("expected 1 result, got %v", resp["results"])
	}
	if resp["query"] != "test" {
		t.Fatalf("expected query 'test', got %v", resp["query"])
	}
}

func TestHandleSubscriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
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
	defer srv.Close()

	app := setupTestAppWithMock(t, srv.URL)
	app.cfg.LocalCredentials = true
	app.cfg.Email = "u"
	app.cfg.Password = "p"
	app.tokenCache.Set(podimo.TokenKey("u", "p"), "fake-token", time.Hour)

	router := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/subscriptions", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	results, ok := resp["results"].([]interface{})
	if !ok || len(results) != 1 {
		t.Fatalf("expected 1 result, got %v", resp["results"])
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
