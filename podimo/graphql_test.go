package podimo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGraphQLClient_Query_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("server error"))
	}))
	defer srv.Close()
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "bad query"},
			},
		})
	}))
	defer srv.Close()
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podcast": map[string]interface{}{"title": "Hello"},
			},
		})
	}))
	defer srv.Close()
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
