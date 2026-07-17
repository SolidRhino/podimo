package podimo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGraphQLClient_Query_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("server error"))
	}))
	t.Cleanup(srv.Close)
	c := NewGraphQLClient(srv.URL, srv.Client())
	var result map[string]interface{}
	err := c.Query(context.Background(), nil, "query {}", nil, &result)
	if err == nil {
		t.Fatal("expected error for non-200")
	}
}

func TestGraphQLClient_Query_GraphQLError(t *testing.T) {
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
	c := NewGraphQLClient(srv.URL, srv.Client())
	var result map[string]interface{}
	err := c.Query(context.Background(), nil, "query {}", nil, &result)
	if err == nil {
		t.Fatal("expected error for GraphQL error")
	}
	if err.Error() != "graphql: bad query" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGraphQLClient_Query_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcast": map[string]interface{}{"title": "Hello"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewGraphQLClient(srv.URL, srv.Client())
	var result map[string]interface{}
	err := c.Query(context.Background(), nil, "query {}", nil, &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	title, _ := result["podcast"].(map[string]interface{})["title"].(string)
	if title != "Hello" {
		t.Fatalf("unexpected title: %s", title)
	}
}

func TestGraphQLClient_Query_LargeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{\"data\":{"))
		_, _ = w.Write(bytes.Repeat([]byte("x"), 11*1024*1024))
		_, _ = w.Write([]byte("}}"))
	}))
	t.Cleanup(srv.Close)
	c := NewGraphQLClient(srv.URL, srv.Client())
	var result map[string]interface{}
	err := c.Query(context.Background(), nil, "query {}", nil, &result)
	if err == nil {
		t.Fatal("expected error for large response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected exceeds error, got %v", err)
	}
}

func TestGraphQLClient_Query_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewGraphQLClient(srv.URL, srv.Client())
	var result map[string]interface{}
	err := c.Query(context.Background(), nil, "query {}", nil, &result)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	httpErr, ok := err.(*HTTPStatusError)
	if !ok {
		t.Fatalf("expected *HTTPStatusError, got %T", err)
	}
	if httpErr.StatusCode != 401 {
		t.Fatalf("expected status 401, got %d", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Body, "unauthorized") {
		t.Fatalf("expected body to contain 'unauthorized', got %s", httpErr.Body)
	}
}

func TestGraphQLClient_Query_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the caller's context deadline.
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
	}))
	t.Cleanup(srv.Close)
	c := NewGraphQLClient(srv.URL, srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var result map[string]interface{}
	err := c.Query(ctx, nil, "query {}", nil, &result)
	if err == nil {
		t.Fatal("expected context deadline exceeded error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}
