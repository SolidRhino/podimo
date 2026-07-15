package podimo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failNTimesTransport struct {
	n    int
	base http.RoundTripper
}

func (t *failNTimesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.n > 0 {
		t.n--
		return nil, fmt.Errorf("simulated network error")
	}
	return t.base.RoundTrip(req)
}

func TestChunks(t *testing.T) {
	items := []interface{}{1, 2, 3, 4, 5, 6, 7}
	result := chunks(items, 3)
	if len(result) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(result))
	}
	if len(result[0]) != 3 || len(result[1]) != 3 || len(result[2]) != 1 {
		t.Fatalf("unexpected chunk sizes")
	}
}

func TestExtractAudioURL(t *testing.T) {
	ep := Episode{
		Audio: EpisodeAudio{
			URL:      "http://example.com/audio.mp3",
			Duration: 123.0,
		},
	}
	url, dur := ExtractAudioURL(ep)
	if url != "http://example.com/audio.mp3" || dur != 123 {
		t.Fatalf("unexpected audio extraction: %s %d", url, dur)
	}
}

func TestExtractAudioURL_StreamMedia(t *testing.T) {
	ep := Episode{
		StreamMedia: EpisodeAudio{
			URL:      "http://example.com/hls-media/123/main.m3u8",
			Duration: 456.0,
		},
	}
	url, dur := ExtractAudioURL(ep)
	expected := "http://example.com/audios/123.mp3"
	if url != expected || dur != 456 {
		t.Fatalf(`expected %s, got %s`, expected, url)
	}
}

func TestURLHeadInfo_Cache(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	c.Set("ep1", map[string]interface{}{"length": "12345", "type": "audio/mpeg"}, time.Hour)
	cl, ct, err := URLHeadInfo(context.Background(), nil, "ep1", "", nil, c, time.Hour, nil)
	if err != nil || cl != "12345" || ct != "audio/mpeg" {
		t.Fatalf("expected cache hit")
	}
}

func TestURLHeadInfo_Network(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("expected HEAD, got %s", r.Method)
		}
		w.Header().Set("Content-Length", "42")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	cl, ct, err := URLHeadInfo(context.Background(), srv.Client(), "ep1", srv.URL, nil, c, time.Hour, nil)
	if err != nil || cl != "42" || ct != "audio/mpeg" {
		t.Fatalf("unexpected result: %v %s %s", err, cl, ct)
	}
	// second call should use cache without a network request
	cl2, ct2, err2 := URLHeadInfo(context.Background(), nil, "ep1", "", nil, c, time.Hour, nil)
	if err2 != nil || cl2 != "42" || ct2 != "audio/mpeg" {
		t.Fatalf("expected cache hit")
	}
}

func TestURLHeadInfo_RetrySuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("expected HEAD, got %s", r.Method)
		}
		w.Header().Set("Content-Length", "999")
		w.Header().Set("Content-Type", "audio/mp4")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := srv.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &failNTimesTransport{n: 2, base: base}

	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	cl, ct, err := URLHeadInfo(context.Background(), client, "retry-ep", srv.URL, nil, c, time.Hour, nil)
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if cl != "999" {
		t.Fatalf("expected Content-Length 999, got %s", cl)
	}
	if ct != "audio/mp4" {
		t.Fatalf("expected audio/mp4, got %s", ct)
	}
}

func TestURLHeadInfo_RetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("expected HEAD, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := srv.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &failNTimesTransport{n: 5, base: base}

	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	_, _, err := URLHeadInfo(context.Background(), client, "fail-ep", srv.URL, nil, c, time.Hour, nil)
	if err == nil {
		t.Fatalf("expected error after all retries exhausted")
	}
}

func TestPodcastsToRss_Basic(t *testing.T) {
	data := &PodcastData{
		Podcast: Podcast{
			Title:       "Test",
			Description: "Desc",
			AuthorName:  "Author",
			Language:    "en",
			Images: struct {
				CoverImageURL string `json:"coverImageUrl"`
			}{CoverImageURL: "http://cover.jpg"},
		},
		Episodes: []Episode{
			{
				ID:              "ep1",
				Title:           "Episode 1",
				Description:     "Desc 1",
				PublishDatetime: "2023-01-01T00:00:00Z",
				Audio: EpisodeAudio{
					URL:      "http://audio.mp3",
					Duration: 60.0,
				},
			},
		},
	}
	dir := t.TempDir()
	hc, _ := NewFileCache(dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	xml, err := PodcastsToRss(context.Background(), "12345678-1234-1234-1234-123456789abc", data, "en-US", hc, false, time.Hour, srv.Client(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(xml)
	if !strings.Contains(s, "<title>Test</title>") {
		t.Fatalf("expected channel title")
	}
	if !strings.Contains(s, "<itunes:block>yes</itunes:block>") {
		t.Fatalf("expected iTunes block")
	}
	if !strings.Contains(s, "<enclosure") {
		t.Fatalf("expected enclosure")
	}
}

func TestPodcastsToRss_ContextCancel(t *testing.T) {
	data := &PodcastData{
		Podcast: Podcast{
			Title:       "Test",
			Description: "Desc",
			AuthorName:  "Author",
			Language:    "en",
			Images: struct {
				CoverImageURL string `json:"coverImageUrl"`
			}{CoverImageURL: "http://cover.jpg"},
		},
		Episodes: []Episode{
			{
				ID:              "ep1",
				Title:           "Episode 1",
				Description:     "Desc 1",
				PublishDatetime: "2023-01-01T00:00:00Z",
				Audio: EpisodeAudio{
					URL:      "http://audio.mp3",
					Duration: 60.0,
				},
			},
		},
	}
	dir := t.TempDir()
	hc, _ := NewFileCache(dir)
	hc.Set("ep1", map[string]interface{}{"length": "100", "type": "audio/mpeg"}, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PodcastsToRss(ctx, "12345678-1234-1234-1234-123456789abc", data, "en-US", hc, false, time.Hour, nil, nil)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestURLHeadInfo_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("expected HEAD, got %s", r.Method)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := srv.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &failNTimesTransport{n: 5, base: base}

	dir := t.TempDir()
	hc, _ := NewFileCache(dir)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	_, _, err := URLHeadInfo(ctx, client, "cancel-ep", srv.URL, nil, hc, time.Hour, nil)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestURLHeadInfo_404NoCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	_, _, err := URLHeadInfo(context.Background(), srv.Client(), "ep404", srv.URL, nil, c, time.Hour, nil)
	if err == nil {
		t.Fatalf("expected error for 404")
	}
	// cache must be empty — no poisoning
	if _, ok := c.Get("ep404"); ok {
		t.Fatalf("expected cache to be empty after 404")
	}
}

func TestURLHeadInfo_500RetriesThenFails(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	_, _, err := URLHeadInfo(context.Background(), srv.Client(), "ep500", srv.URL, nil, c, time.Hour, nil)
	if err == nil {
		t.Fatalf("expected error for persistent 500")
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts (retries), got %d", calls)
	}
	if _, ok := c.Get("ep500"); ok {
		t.Fatalf("expected cache to be empty after 500")
	}
}

func TestURLHeadInfo_500Then200Retries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", "42")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, _ := NewFileCache(dir)
	cl, ct, err := URLHeadInfo(context.Background(), srv.Client(), "ep500to200", srv.URL, nil, c, time.Hour, nil)
	if err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if cl != "42" || ct != "audio/mpeg" {
		t.Fatalf("unexpected result: %s %s", cl, ct)
	}
	if _, ok := c.Get("ep500to200"); !ok {
		t.Fatalf("expected cache to be populated after successful retry")
	}
}
