package podimo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

func TestNewPodimoClient_Validation(t *testing.T) {
	_, err := NewPodimoClient("", "", "nl", "nl-NL", nil, nil, nil, nil)
	if err == nil || err.Error() != "empty username or password" {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestNewPodimoClient_LoadsCachedToken(t *testing.T) {
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	_ = tc.Set(TokenKey("user", "pass"), "cached-token", time.Hour)
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
	t.Cleanup(srv.Close)
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
	t.Cleanup(srv.Close)
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
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	// Ensure token is set so GetPodcasts doesn't try to use the mock for login
	_, _ = client.Login(context.Background())

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
	t.Cleanup(srv.Close)
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
	if results[0].Title != "Podcast 1" {
		t.Fatalf("unexpected title: %v", results[0].Title)
	}
}

func TestPodimoClient_SearchPodcasts_AllVariantsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "bad query"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	client.token = "fake" // skip login
	results, err := client.SearchPodcasts(context.Background(), "test")
	if err == nil {
		t.Fatalf("expected error when all variants fail, got nil")
	}
	if results != nil {
		t.Fatalf("expected nil results when all variants fail, got %v", results)
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
	t.Cleanup(srv.Close)
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
	data := &PodcastData{Podcast: Podcast{Title: "Nice Podcast"}}
	if GetPodcastName(data) != "Nice Podcast" {
		t.Fatalf("expected Nice Podcast")
	}
	if GetPodcastName(&PodcastData{}) != "Unknown" {
		t.Fatalf("expected Unknown")
	}
}

func TestGetPodcasts_GQLErrorNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "Podcast not found"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srv.URL, srv.Client())
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	client.token = "fake"
	_, err := client.GetPodcasts(context.Background(), "pid", time.Hour)
	if _, ok := err.(*PodcastNotFoundError); !ok {
		t.Fatalf("expected PodcastNotFoundError, got %T %v", err, err)
	}
}

func TestGetPodcasts_NetworkErrorNot404(t *testing.T) {
	// Point to a non-listening port to force a network error.
	gl := NewGraphQLClient("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond})
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	client.token = "fake"
	_, err := client.GetPodcasts(context.Background(), "pid", time.Hour)
	if err == nil {
		t.Fatalf("expected error")
	}
	if _, ok := err.(*PodcastNotFoundError); ok {
		t.Fatalf("network error must not be misclassified as PodcastNotFoundError")
	}
}

func TestMapAuthError_HTTP401(t *testing.T) {
	err := &HTTPStatusError{StatusCode: 401, Body: "unauthorized"}
	mapped := mapAuthError(err)
	authErr, ok := mapped.(*AuthenticationError)
	if !ok {
		t.Fatalf("expected *AuthenticationError, got %T", mapped)
	}
	if !strings.Contains(authErr.Error(), "401") {
		t.Fatalf("expected error to mention 401, got %s", authErr.Error())
	}
}

func TestMapAuthError_NonAuthHTTPStatus(t *testing.T) {
	err := &HTTPStatusError{StatusCode: 500, Body: "server error"}
	mapped := mapAuthError(err)
	if _, ok := mapped.(*AuthenticationError); ok {
		t.Fatalf("500 must not be promoted to AuthenticationError")
	}
}

// episodesPage builds a PodcastData-shaped GraphQL "data" payload with n
// episodes whose IDs are generated by idFn(i).
func episodesPage(n int, idFn func(i int) string) map[string]interface{} {
	eps := make([]interface{}, 0, n)
	for i := range n {
		eps = append(eps, map[string]interface{}{
			"id":          idFn(i),
			"title":       "Episode",
			"audio":       map[string]interface{}{"url": "https://example.com/a.mp3", "duration": 100},
			"streamMedia": map[string]interface{}{},
		})
	}
	return map[string]interface{}{"podcast": map[string]interface{}{"title": "Test"}, "episodes": eps}
}

// graphqlSeqServer returns a server that emits the given data payloads in
// order, then an empty-data response forever after.
func graphqlSeqServer(t *testing.T, payloads []map[string]interface{}) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if idx < len(payloads) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": payloads[idx]})
			idx++
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
	}))
}

// newAuthedClient builds a PodimoClient whose token is already set, so
// GetPodcasts talks directly to the provided server without a login handshake.
func newAuthedClient(t *testing.T, srvURL string) *PodimoClient {
	t.Helper()
	dir := t.TempDir()
	tc, _ := NewFileCache(dir)
	pc, _ := NewFileCache(dir)
	gl := NewGraphQLClient(srvURL, nil)
	client, _ := NewPodimoClient("u", "p", "nl", "nl-NL", gl, tc, pc, nil)
	client.token = "preset-token"
	return client
}

func TestGetPodcasts_DedupStopsPagination(t *testing.T) {
	// Every page returns the same 100 episode IDs — the dedup guard must
	// stop pagination after the first page without hanging.
	srv := graphqlSeqServer(t, []map[string]interface{}{
		episodesPage(100, func(i int) string { return fmt.Sprintf("ep-%d", i) }),
		episodesPage(100, func(i int) string { return fmt.Sprintf("ep-%d", i) }),
		episodesPage(100, func(i int) string { return fmt.Sprintf("ep-%d", i) }),
	})
	t.Cleanup(srv.Close)
	client := newAuthedClient(t, srv.URL)

	done := make(chan struct{})
	var data *PodcastData
	go func() {
		defer close(done)
		var err error
		data, err = client.GetPodcasts(context.Background(), "pid", 0)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GetPodcasts hung; dedup guard failed to stop pagination")
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
	// The dedup guard stops pagination once a page repeats an ID. Only the
	// first (non-repeating) page's episodes survive — at most `limit` (100).
	if len(data.Episodes) > 100 {
		t.Fatalf("expected at most 100 episodes after dedup, got %d", len(data.Episodes))
	}
	seen := make(map[string]struct{}, len(data.Episodes))
	for _, ep := range data.Episodes {
		if _, dup := seen[ep.ID]; dup {
			t.Fatalf("duplicate episode ID in result: %s", ep.ID)
		}
		seen[ep.ID] = struct{}{}
	}
}

func TestGetPodcasts_PageCapTerminates(t *testing.T) {
	// Server returns a fresh full page of 100 distinct episodes every request.
	// The maxPages cap must terminate pagination after 200 pages.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		// Use the request offset to produce distinct IDs per page.
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vars, _ := body["variables"].(map[string]interface{})
		offset := 0
		if v, ok := vars["offset"].(float64); ok {
			offset = int(v)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": episodesPage(100, func(i int) string { return fmt.Sprintf("ep-%d", offset+i) }),
		})
	}))
	t.Cleanup(srv.Close)
	client := newAuthedClient(t, srv.URL)

	done := make(chan struct{})
	go func() {
		defer close(done)
		data, err := client.GetPodcasts(context.Background(), "pid", 0)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		seen := make(map[string]string)
		for _, ep := range data.Episodes {
			if _, dup := seen[ep.ID]; dup {
				t.Errorf("duplicate episode ID in result: %s", ep.ID)
			}
			seen[ep.ID] = ep.ID
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("GetPodcasts hung; page cap failed to terminate pagination")
	}
}

func TestGetFollowedPodcasts_Extended(t *testing.T) {
	// Extended variant succeeds: metadata fields populated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsFollowed": []interface{}{
					map[string]interface{}{
						"id":            "p1",
						"title":         "Has Count",
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
	client := newAuthedClient(t, srv.URL)

	podcasts, err := client.GetFollowedPodcasts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(podcasts) != 1 {
		t.Fatalf("expected 1 podcast, got %d", len(podcasts))
	}
	if podcasts[0].EpisodeCount != 12 {
		t.Fatalf("expected EpisodeCount 12, got %d", podcasts[0].EpisodeCount)
	}
	if podcasts[0].LatestEpisode.PublishDatetime != "2024-05-01T00:00:00Z" {
		t.Fatalf("expected latest publish date, got %q", podcasts[0].LatestEpisode.PublishDatetime)
	}
}

func TestGetFollowedPodcasts_FallbackMinimal(t *testing.T) {
	// First variant (extended) returns a GraphQL error; second (minimal) succeeds.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if calls == 1 {
			// Schema rejects the extended fields.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []map[string]interface{}{{"message": "Cannot query field \"episodeCount\""}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcastsFollowed": []interface{}{
					map[string]interface{}{
						"id":            "p2",
						"title":         "Minimal",
						"coverImageUrl": "http://cover.jpg",
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	client := newAuthedClient(t, srv.URL)

	podcasts, err := client.GetFollowedPodcasts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(podcasts) != 1 {
		t.Fatalf("expected 1 podcast, got %d", len(podcasts))
	}
	if podcasts[0].Title != "Minimal" {
		t.Fatalf("expected title Minimal, got %q", podcasts[0].Title)
	}
	if podcasts[0].EpisodeCount != 0 {
		t.Fatalf("expected zero EpisodeCount on fallback, got %d", podcasts[0].EpisodeCount)
	}
}
