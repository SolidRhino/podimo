package podimo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"
)

type PodimoError struct {
	Message string
}

func (e PodimoError) Error() string { return e.Message }

type PodcastNotFoundError struct {
	PodimoError
}

type AuthenticationError struct {
	PodimoError
}

func NewPodcastNotFoundError(msg string) *PodcastNotFoundError {
	return &PodcastNotFoundError{PodimoError{Message: msg}}
}

func NewAuthenticationError(msg string) *AuthenticationError {
	return &AuthenticationError{PodimoError{Message: msg}}
}

// mapAuthError returns an *AuthenticationError if err is a GQLError whose message
// indicates an auth failure, otherwise it returns err unchanged.
func mapAuthError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*AuthenticationError); ok {
		return err
	}
	var gqlErr GQLError
	if errors.As(err, &gqlErr) {
		msg := strings.ToLower(gqlErr.Message)
		if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "not authorized") {
			return NewAuthenticationError(gqlErr.Message)
		}
	}
	return err
}

type PodimoClient struct {
	username     string
	password     string
	region       string
	locale       string
	key          string
	token        string
	preauthToken string
	preregID     string
	graphql      *GraphQLClient
	tokenCache   *FileCache
	podcastCache *FileCache
	logger       *slog.Logger
}

func NewPodimoClient(username, password, region, locale string, graphql *GraphQLClient, tokenCache, podcastCache *FileCache, logger *slog.Logger) (*PodimoClient, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("empty username or password")
	}
	if len(username) > 256 || len(password) > 256 {
		return nil, fmt.Errorf("username or password are too long")
	}

	key := TokenKey(username, password)
	var storedToken interface{}
	if tokenCache != nil {
		storedToken, _ = tokenCache.Get(key)
	}

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	client := &PodimoClient{
		username:     username,
		password:     password,
		region:       region,
		locale:       locale,
		key:          key,
		graphql:      graphql,
		tokenCache:   tokenCache,
		podcastCache: podcastCache,
		logger:       logger,
	}
	if str, ok := storedToken.(string); ok {
		client.token = str
	}
	return client, nil
}

func (c *PodimoClient) Token() string { return c.token }
func (c *PodimoClient) Key() string   { return c.key }

func (c *PodimoClient) generateHeaders(authorization string) map[string]string {
	headers := map[string]string{
		"user-os":        "android",
		"user-agent":     "Podimo/2.45.1 build 566/Android 33",
		"user-version":   "2.45.1",
		"user-locale":    c.locale,
		"user-unique-id": RandomHexID(16),
		"content-type":   "application/json",
	}
	if authorization != "" {
		headers["authorization"] = authorization
	}
	return headers
}

func RandomHexID(length int) string {
	const hexChars = "1234567890abcdef"
	b := make([]byte, length)
	for i := range b {
		b[i] = hexChars[rand.IntN(len(hexChars))]
	}
	return string(b)
}

func randomFlyerID() string {
	a := rand.Int64N(8999999999999) + 1000000000000
	b := rand.Int64N(8999999999999) + 1000000000000
	return fmt.Sprintf("%d-%d", a, b)
}

func TokenKey(username, password string) string {
	h := sha256.New()
	h.Write([]byte(username))
	h.Write([]byte("~"))
	h.Write([]byte(password))
	return hex.EncodeToString(h.Sum(nil))
}

func (c *PodimoClient) getPreregisterToken(ctx context.Context) error {
	headers := c.generateHeaders("")
	query := `query AuthorizationPreregisterUser($locale: String!, $referenceUser: String, $countryCode: String, $appsFlyerId: String) {
		tokenWithPreregisterUser(
			locale: $locale
			referenceUser: $referenceUser
			countryCode: $countryCode
			source: MOBILE
			appsFlyerId: $appsFlyerId
			currentCountry: $countryCode
		) {
			token
		}
	}`
	variables := map[string]interface{}{
		"locale":      c.locale,
		"countryCode": c.region,
		"appsFlyerId": randomFlyerID(),
	}

	var result map[string]interface{}
	if err := c.graphql.Query(ctx, headers, query, variables, &result); err != nil {
		return err
	}

	tokenWithPreregisterUser, ok := result["tokenWithPreregisterUser"].(map[string]interface{})
	if !ok || tokenWithPreregisterUser == nil {
		return fmt.Errorf("Podimo did not provide a tokenWithPreregisterUser")
	}
	token, ok := tokenWithPreregisterUser["token"].(string)
	if !ok || token == "" {
		return fmt.Errorf("Podimo did not provide a tokenWithPreregisterUser token")
	}
	c.preauthToken = token
	return nil
}

func (c *PodimoClient) getOnboardingID(ctx context.Context) error {
	headers := c.generateHeaders(c.preauthToken)
	query := `query OnboardingQuery {
		userOnboardingFlow {
			id
		}
	}`
	var result map[string]interface{}
	if err := c.graphql.Query(ctx, headers, query, nil, &result); err != nil {
		return err
	}

	userOnboardingFlow, ok := result["userOnboardingFlow"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("Podimo did not provide a userOnboardingFlow")
	}
	id, ok := userOnboardingFlow["id"].(string)
	if !ok {
		return fmt.Errorf("Podimo did not provide an onboarding ID")
	}
	c.preregID = id
	return nil
}

func (c *PodimoClient) Login(ctx context.Context) (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	return c.login(ctx)
}

// RefreshToken invalidates the cached token (in-memory and on disk) and performs
// a fresh login. Used to recover from a stale token that a downstream call
// rejected with an AuthenticationError.
func (c *PodimoClient) RefreshToken(ctx context.Context) error {
	c.token = ""
	if c.tokenCache != nil {
		_ = c.tokenCache.Delete(c.key)
	}
	_, err := c.login(ctx)
	return err
}

// login performs the 3-step preregister → onboarding → authorize flow and stores
// the resulting token. It assumes the caller has already decided a fresh login
// is required (no cached-token guard).
func (c *PodimoClient) login(ctx context.Context) (string, error) {
	if err := c.getPreregisterToken(ctx); err != nil {
		return "", err
	}
	if err := c.getOnboardingID(ctx); err != nil {
		return "", err
	}

	headers := c.generateHeaders(c.preauthToken)
	query := `query AuthorizationAuthorize($email: String!, $password: String!, $locale: String!, $preregisterId: String) {
		tokenWithCredentials(
			email: $email
			password: $password
			locale: $locale
			preregisterId: $preregisterId
		) {
			token
		}
	}`
	variables := map[string]interface{}{
		"email":         c.username,
		"password":      c.password,
		"locale":        c.locale,
		"preregisterId": c.preregID,
	}

	var result map[string]interface{}
	if err := c.graphql.Query(ctx, headers, query, variables, &result); err != nil {
		return "", err
	}

	tokenWithCredentials, ok := result["tokenWithCredentials"].(map[string]interface{})
	if !ok || tokenWithCredentials == nil {
		return "", NewAuthenticationError("Invalid Podimo credentials, did not receive tokenWithCredentials")
	}
	token, ok := tokenWithCredentials["token"].(string)
	if !ok || token == "" {
		return "", NewAuthenticationError("Invalid Podimo credentials, did not receive token")
	}
	c.token = token
	return token, nil
}

func (c *PodimoClient) GetPodcasts(ctx context.Context, podcastID string, cacheTTL time.Duration) (*PodcastData, error) {
	if cached, ok := c.podcastCache.Get(podcastID); ok {
		if data := podcastDataFromCache(cached); data != nil {
			return data, nil
		}
	}

	headers := c.generateHeaders(c.token)
	query := `query ChannelEpisodesQuery($podcastId: String!, $limit: Int!, $offset: Int!, $sorting: PodcastEpisodeSorting) {
		episodes: podcastEpisodes(
			podcastId: $podcastId
			converted: true
			published: true
			limit: $limit
			offset: $offset
			sorting: $sorting
		) {
			...EpisodeBase
		}
		podcast: podcastById(podcastId: $podcastId) {
			title
			description
			webAddress
			authorName
			language
			images {
				coverImageUrl
			}
		}
	}
	fragment EpisodeBase on PodcastEpisode {
		id
		artist
		podcastName
		imageUrl
		description
		datetime
		publishDatetime
		title
		audio {
			url
			duration
		}
		streamMedia {
			duration
			url
		}
	}`

	limit := 100
	offset := 0
	var fullData PodcastData

	for {
		variables := map[string]interface{}{
			"podcastId": podcastID,
			"limit":     limit,
			"offset":    offset,
			"sorting":   "PUBLISHED_DESCENDING",
		}

		var page PodcastData
		if err := c.graphql.Query(ctx, headers, query, variables, &page); err != nil {
			mapped := mapAuthError(err)
			if _, ok := mapped.(*AuthenticationError); ok {
				return nil, mapped
			}
			var gqlErr GQLError
			if errors.As(err, &gqlErr) {
				msg := strings.ToLower(gqlErr.Message)
				if strings.Contains(msg, "not found") {
					return nil, NewPodcastNotFoundError(fmt.Sprintf("Podcast %s not found", podcastID))
				}
			}
			return nil, fmt.Errorf("fetch episodes for %s: %w", podcastID, err)
		}

		if offset == 0 {
			fullData = page
		} else {
			fullData.Episodes = append(fullData.Episodes, page.Episodes...)
		}

		if len(page.Episodes) < limit {
			break
		}
		offset += limit
	}

	if err := c.podcastCache.Set(podcastID, &fullData, cacheTTL); err != nil {
		c.logger.Error("podcast cache write", "error", err)
	}
	return &fullData, nil
}

// podcastDataFromCache converts a cached value back to *PodcastData. The
// FileCache stores values as interface{}; after a JSON round-trip through disk
// they come back as map[string]interface{}, so we re-marshal and unmarshal into
// the typed struct. Values already of type *PodcastData (in-memory hits) are
// returned directly.
func podcastDataFromCache(cached interface{}) *PodcastData {
	if cached == nil {
		return nil
	}
	if d, ok := cached.(*PodcastData); ok {
		return d
	}
	if d, ok := cached.(PodcastData); ok {
		return &d
	}
	if m, ok := cached.(map[string]interface{}); ok {
		data, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		var pd PodcastData
		if err := json.Unmarshal(data, &pd); err != nil {
			return nil
		}
		return &pd
	}
	return nil
}

func (c *PodimoClient) SearchPodcasts(ctx context.Context, query string) ([]SearchResult, error) {
	if c.token == "" {
		if _, err := c.Login(ctx); err != nil {
			return nil, err
		}
	}

	headers := c.generateHeaders(c.token)

	variants := []struct {
		query     string
		variables map[string]interface{}
		resultKey string
	}{
		{
			query: `query PodcastsAutocomplete($search: String!) {
				podcastsAutocomplete(search: $search) {
					id
					title
					coverImageUrl
					authorName
					description
				}
			}`,
			variables: map[string]interface{}{"search": query},
			resultKey: "podcastsAutocomplete",
		},
		{
			query: `query PodcastsAutocomplete($search: String!) {
				podcastsAutocomplete(search: $search) {
					id
					title
				}
			}`,
			variables: map[string]interface{}{"search": query},
			resultKey: "podcastsAutocomplete",
		},
		{
			query: `query SearchPodcasts($search: String!) {
				searchPodcasts(search: $search) {
					id
					title
					coverImageUrl
				}
			}`,
			variables: map[string]interface{}{"search": query},
			resultKey: "searchPodcasts",
		},
	}

	var lastErr error
	for _, variant := range variants {
		var result struct {
			Podcasts []SearchResult `json:"podcastsAutocomplete"`
			Search   []SearchResult `json:"searchPodcasts"`
		}
		if err := c.graphql.Query(ctx, headers, variant.query, variant.variables, &result); err != nil {
			lastErr = err
			continue
		}
		var out []SearchResult
		if variant.resultKey == "podcastsAutocomplete" && result.Podcasts != nil {
			out = result.Podcasts
		} else if variant.resultKey == "searchPodcasts" && result.Search != nil {
			out = result.Search
		}
		if out != nil {
			return out, nil
		}
	}

	if lastErr != nil {
		c.logger.Warn("SearchPodcasts: all variants failed", "error", lastErr)
		return nil, mapAuthError(lastErr)
	}
	return []SearchResult{}, nil
}
func (c *PodimoClient) GetFollowedPodcasts(ctx context.Context) ([]FollowedPodcast, error) {
	if c.token == "" {
		if _, err := c.Login(ctx); err != nil {
			return nil, err
		}
	}

	headers := c.generateHeaders(c.token)
	query := `query PodcastsFollowed {
		podcastsFollowed {
			id
			title
			coverImageUrl
		}
	}`

	var result struct {
		Podcasts []FollowedPodcast `json:"podcastsFollowed"`
	}
	if err := c.graphql.Query(ctx, headers, query, nil, &result); err != nil {
		return nil, mapAuthError(err)
	}

	return result.Podcasts, nil
}

func GetPodcastName(data *PodcastData) string {
	if data == nil {
		return "Unknown"
	}
	if data.Podcast.Title == "" {
		return "Unknown"
	}
	return data.Podcast.Title
}
