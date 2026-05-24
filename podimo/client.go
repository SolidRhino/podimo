package podimo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
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
}

func NewPodimoClient(username, password, region, locale string, graphql *GraphQLClient, tokenCache, podcastCache *FileCache) (*PodimoClient, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("empty username or password")
	}
	if len(username) > 256 || len(password) > 256 {
		return nil, fmt.Errorf("username or password are too long")
	}

	key := TokenKey(username, password)
	storedToken, _ := tokenCache.Get(key)

	client := &PodimoClient{
		username:     username,
		password:     password,
		region:       region,
		locale:       locale,
		key:          key,
		graphql:      graphql,
		tokenCache:   tokenCache,
		podcastCache: podcastCache,
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
		"user-unique-id": randomHexID(16),
		"content-type":   "application/json",
	}
	if authorization != "" {
		headers["authorization"] = authorization
	}
	return headers
}

func randomHexID(length int) string {
	const hexChars = "1234567890abcdef"
	b := make([]byte, length)
	for i := range b {
		b[i] = hexChars[rand.Intn(len(hexChars))]
	}
	return string(b)
}

func randomFlyerID() string {
	a := rand.Int63n(8999999999999) + 1000000000000
	b := rand.Int63n(8999999999999) + 1000000000000
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
	variables := map[string]interface{}{
		"locale":      c.locale,
		"countryCode": c.region,
		"appsFlyerId": randomFlyerID(),
	}

	var result map[string]interface{}
	if err := c.graphql.Query(ctx, headers, query, variables, &result); err != nil {
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

func (c *PodimoClient) GetPodcasts(ctx context.Context, podcastID string, cacheTTL time.Duration) (map[string]interface{}, error) {
	if cached, ok := c.podcastCache.Get(podcastID); ok {
		if podcast, ok := cached.(map[string]interface{}); ok {
			return podcast, nil
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
	var fullResult map[string]interface{}

	for {
		variables := map[string]interface{}{
			"podcastId": podcastID,
			"limit":     limit,
			"offset":    offset,
			"sorting":   "PUBLISHED_DESCENDING",
		}

		var result map[string]interface{}
		if err := c.graphql.Query(ctx, headers, query, variables, &result); err != nil {
			if _, ok := err.(*AuthenticationError); ok {
				return nil, err
			}
			if strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "unauthenticated") {
				return nil, NewAuthenticationError(err.Error())
			}
			return nil, NewPodcastNotFoundError(fmt.Sprintf("Podcast %s not found or empty response", podcastID))
		}

		if offset == 0 {
			fullResult = result
		} else {
			prevEpisodes, _ := fullResult["episodes"].([]interface{})
			newEpisodes, _ := result["episodes"].([]interface{})
			fullResult["episodes"] = append(prevEpisodes, newEpisodes...)
		}

		episodes, _ := result["episodes"].([]interface{})
		if len(episodes) < limit {
			break
		}
		offset += limit
	}

	c.podcastCache.Set(podcastID, fullResult, cacheTTL)
	return fullResult, nil
}

func (c *PodimoClient) SearchPodcasts(ctx context.Context, query string) ([]map[string]interface{}, error) {
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
		var result map[string]interface{}
		if err := c.graphql.Query(ctx, headers, variant.query, variant.variables, &result); err != nil {
			lastErr = err
			continue
		}
		podcasts, ok := result[variant.resultKey].([]interface{})
		if !ok {
			continue
		}
		var out []map[string]interface{}
		for _, p := range podcasts {
			if pm, ok := p.(map[string]interface{}); ok {
				out = append(out, pm)
			}
		}
		return out, nil
	}

	if lastErr != nil {
		log.Printf("SearchPodcasts: all variants failed, last error: %v", lastErr)
	}
	return []map[string]interface{}{}, nil
}

func (c *PodimoClient) GetFollowedPodcasts(ctx context.Context) ([]map[string]interface{}, error) {
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

	var result map[string]interface{}
	if err := c.graphql.Query(ctx, headers, query, nil, &result); err != nil {
		return nil, err
	}

	podcasts, ok := result["podcastsFollowed"].([]interface{})
	if !ok {
		return []map[string]interface{}{}, nil
	}

	var out []map[string]interface{}
	for _, p := range podcasts {
		if pm, ok := p.(map[string]interface{}); ok {
			out = append(out, pm)
		}
	}
	return out, nil
}

func GetPodcastName(podcast map[string]interface{}) string {
	podcastData, ok := podcast["podcast"].(map[string]interface{})
	if !ok {
		return "Unknown"
	}
	title, _ := podcastData["title"].(string)
	if title == "" {
		return "Unknown"
	}
	return title
}
