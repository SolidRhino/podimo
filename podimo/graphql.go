package podimo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type GraphQLClient struct {
	endpoint string
	client   *http.Client
}

func NewGraphQLClient(endpoint string, client *http.Client) *GraphQLClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &GraphQLClient{
		endpoint: endpoint,
		client:   client,
	}
}

type gqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GQLError      `json:"errors"`
}

type GQLError struct {
	Message string `json:"message"`
}

func (e GQLError) Error() string { return "graphql: " + e.Message }

func (c *GraphQLClient) Query(ctx context.Context, headers map[string]string, query string, variables map[string]interface{}, resp interface{}) error {
	body := gqlRequest{Query: query, Variables: variables}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json; charset=utf-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	const maxResponseSize = 10 * 1024 * 1024 // 10MB
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if len(raw) > maxResponseSize {
		return fmt.Errorf("graphql response exceeds %d bytes", maxResponseSize)
	}

	if res.StatusCode != http.StatusOK {
		body := raw
		if len(body) > 500 {
			body = body[:500]
		}
		return fmt.Errorf("graphql: non-200 status: %d, body: %s", res.StatusCode, body)
	}

	var gr gqlResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(gr.Errors) > 0 {
		return gr.Errors[0]
	}

	return json.Unmarshal(gr.Data, resp)
}
