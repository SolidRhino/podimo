package podimo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mockGraphQLServer(t *testing.T, responses []map[string]interface{}) *httptest.Server {
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if idx < len(responses) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": responses[idx],
			})
			idx++
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{},
			})
		}
	}))
}

func TestNewPodimoClient_Validation(t *testing.T) {
	_, err := NewPodimoClient("", "", "nl", "nl-NL", nil, nil, nil, nil)
	if err == nil || err.Error() != "empty username or password" {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestNewPodimoClient_LoadsCachedToken(t *testing.T) {
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	tc.Set(TokenKey("user", "pass"), "cached-token", time.Hour)
	gl := NewGraphQLClient("http://localhost", nil)
	pc, _ := NewFileCache(dir)
	client, err := NewPodimoClient("user", "pass", "nl", "nl-NL", gl, tc, pc, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.Token() != "cached-token" {
		t.Fatalf("expected cached token, got %s", client.Token())
	}
}

func TestNewPodimoClient_NilTokenCache(t *testing.T) {
	dir := t.TempDir()
	gl := NewGraphQLClient("http://localhost", nil)
	pc, _ := NewFileCache(dir)
	client, err := NewPodimoClient("user", "pass", "nl", "nl-NL", gl, nil, pc, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.Token() != "" {
		t.Fatalf("expected empty token when nil cache, got %s", client.Token())
	}
}

func TestPodimoClient_Login(t *testing.T) {
	srv := mockGraphQLServer(t, []map[string]interface{}{
		{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}},
		{"userOnboardingFlow": map[string]interface{}{"id": "oid"}},
		{"tokenWithCredentials": map[string]interface{}{"token": "final"}},
	})
	defer srv.Close()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	token, err := client.Login(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "final" {
		t.Fatalf("expected final token, got %s", token)
	}
	if client.Token() != "final" {
		t.Fatalf("expected client token to be set")
	}
}

func TestPodimoClient_Login_InvalidCredentials(t *testing.T) {
	srv := mockGraphQLServer(t, []map[string]interface{}{
		{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}},
		{"userOnboardingFlow": map[string]interface{}{"id": "oid"}},
		{"tokenWithCredentials": nil},
	})
	defer srv.Close()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	_, err := client.Login(context.Background())
	if _, ok := err.(*AuthenticationError); !ok {
		t.Fatalf("expected AuthenticationError, got %T %v", err, err)
	}
}

func TestPodimoClient_GetPodcasts_Cache(t *testing.T) {
	srv := mockGraphQLServer(t, []map[string]interface{}{
		{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}},
		{"userOnboardingFlow": map[string]interface{}{"id": "oid"}},
		{"tokenWithCredentials": map[string]interface{}{"token": "final"}},
		{"podcast": map[string]interface{}{"title": "Test"}, "episodes": []interface{}{}},
	})
	defer srv.Close()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	// Ensure token is set so GetPodcasts doesn't try to use the mock for login
	client.Login(context.Background())

	data, err := client.GetPodcasts(context.Background(), "pid", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if GetPodcastName(data) != "Test" {
		t.Fatalf("expected Test podcast, got %s", GetPodcastName(data))
	}
	// second call should use cache
	data2, err2 := client.GetPodcasts(context.Background(), "pid", time.Hour)
	if err2 != nil {
		t.Fatalf("unexpected error on cached call: %v", err2)
	}
	if GetPodcastName(data2) != "Test" {
		t.Fatalf("expected cached result")
	}
}

func TestPodimoClient_SearchPodcasts(t *testing.T) {
	srv := mockGraphQLServer(t, []map[string]interface{}{
		{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}},
		{"userOnboardingFlow": map[string]interface{}{"id": "oid"}},
		{"tokenWithCredentials": map[string]interface{}{"token": "final"}},
		{"podcastsAutocomplete": []interface{}{
			map[string]interface{}{"id": "p1", "title": "Podcast 1"},
		}},
	})
	defer srv.Close()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	results, err := client.SearchPodcasts(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0]["title"] != "Podcast 1" {
		t.Fatalf("unexpected title: %v", results[0]["title"])
	}
}

func TestPodimoClient_SearchPodcasts_AllVariantsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "bad query"},
			},
		})
	}))
	defer srv.Close()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	client.token = "fake" // skip login
	results, err := client.SearchPodcasts(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestPodimoClient_GetFollowedPodcasts(t *testing.T) {
	srv := mockGraphQLServer(t, []map[string]interface{}{
		{"tokenWithPreregisterUser": map[string]interface{}{"token": "pre"}},
		{"userOnboardingFlow": map[string]interface{}{"id": "oid"}},
		{"tokenWithCredentials": map[string]interface{}{"token": "final"}},
		{"podcastsFollowed": []interface{}{
			map[string]interface{}{"id": "p1", "title": "Followed"},
		}},
	})
	defer srv.Close()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	results, err := client.GetFollowedPodcasts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestGetPodcastName(t *testing.T) {
	data := map[string]interface{}{
		"podcast": map[string]interface{}{"title": "Nice Podcast"},
	}
	if GetPodcastName(data) != "Nice Podcast" {
		t.Fatalf("expected Nice Podcast")
	}
	if GetPodcastName(map[string]interface{}{}) != "Unknown" {
		t.Fatalf("expected Unknown")
	}
}
