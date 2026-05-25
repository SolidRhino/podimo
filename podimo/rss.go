package podimo

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eduncan911/podcast"
)

func PodcastsToRss(ctx context.Context, podcastID string, data map[string]interface{}, locale string, headCache *FileCache, publicFeeds bool, headCacheTTL time.Duration, httpClient *http.Client, logger *slog.Logger) ([]byte, error) {
	podcastData, _ := data["podcast"].(map[string]interface{})
	episodes, _ := data["episodes"].([]interface{})

	title := GetPodcastName(data)
	if title == "Unknown" && len(episodes) > 0 {
		if ep, ok := episodes[0].(map[string]interface{}); ok {
			title, _ = ep["podcastName"].(string)
		}
	}
	if title == "" {
		title = "Unknown"
	}

	description, _ := podcastData["description"].(string)
	if description == "" {
		description = title
	}

	p := podcast.New(title, fmt.Sprintf("https://podimo.com/shows/%s", podcastID), description, nil, nil)

	images, _ := podcastData["images"].(map[string]interface{})
	coverImage, _ := images["coverImageUrl"].(string)
	if coverImage == "" && len(episodes) > 0 {
		if ep, ok := episodes[0].(map[string]interface{}); ok {
			coverImage, _ = ep["imageUrl"].(string)
		}
	}
	if coverImage != "" {
		p.AddImage(coverImage)
	}

	language, _ := podcastData["language"].(string)
	if language == "" {
		language = locale
	}
	p.Language = language

	author, _ := podcastData["authorName"].(string)
	if author == "" && len(episodes) > 0 {
		if ep, ok := episodes[0].(map[string]interface{}); ok {
			author, _ = ep["artist"].(string)
		}
	}
	if author != "" {
		p.AddAuthor(author, "")
	}

	if !publicFeeds {
		p.IBlock = "yes"
	}

	// Process episodes in chunks of 5 with parallel HEAD requests
	for _, chunk := range chunks(episodes, 5) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		items := make([]podcast.Item, len(chunk))
		var wg sync.WaitGroup
		for i, ep := range chunk {
			wg.Add(1)
			go func(idx int, raw interface{}) {
				defer wg.Done()
				if ctx.Err() != nil {
					return
				}
				episode, ok := raw.(map[string]interface{})
				if !ok {
					return
				}
				item, err := buildFeedItem(ctx, episode, locale, headCache, headCacheTTL, httpClient)
				if err == nil && item.Title != "" {
					items[idx] = item
				} else if err != nil && logger != nil {
					logger.Debug("Skipped episode", "error", err)
				}
			}(i, ep)
		}
		wg.Wait()
		for _, item := range items {
			if item.Title == "" {
				continue
			}
			if _, err := p.AddItem(item); err != nil && logger != nil {
				logger.Warn("Failed to add RSS item", "title", item.Title, "error", err)
			}
		}
	}

	var buf bytes.Buffer
	if err := p.Encode(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildFeedItem(ctx context.Context, episode map[string]interface{}, locale string, headCache *FileCache, headCacheTTL time.Duration, httpClient *http.Client) (podcast.Item, error) {
	var item podcast.Item

	url, duration := ExtractAudioURL(episode)
	if url == "" {
		return item, fmt.Errorf("no audio URL")
	}

	title, _ := episode["title"].(string)
	description, _ := episode["description"].(string)

	item.Title = title
	item.Description = description

	imageURL, _ := episode["imageUrl"].(string)
	if imageURL != "" {
		item.AddImage(imageURL)
	}

	pubDateStr, _ := episode["publishDatetime"].(string)
	if pubDateStr == "" {
		pubDateStr, _ = episode["datetime"].(string)
	}
	if pubDateStr != "" {
		if t, err := time.Parse(time.RFC3339, pubDateStr); err == nil {
			item.AddPubDate(&t)
		}
	}

	item.AddDuration(int64(duration))

	episodeID, _ := episode["id"].(string)
	item.GUID = episodeID

	headers := map[string]string{
		"user-os":        "android",
		"user-agent":     "Podimo/2.45.1 build 566/Android 33",
		"user-version":   "2.45.1",
		"user-locale":    locale,
		"user-unique-id": randomHexID(16),
	}

	headCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	contentLength, contentType, err := URLHeadInfo(headCtx, httpClient, episodeID, url, headers, headCache, headCacheTTL)
	if err != nil {
		// Graceful fallback: use defaults so one bad HEAD doesn't abort the whole feed
		contentLength = "0"
		contentType = "audio/mpeg"
	}

	lengthInt, _ := strconv.ParseInt(contentLength, 10, 64)
	if lengthInt <= 0 {
		lengthInt = 0
	}

	encType := enclosureTypeFromContentType(contentType)
	item.AddEnclosure(url, encType, lengthInt)

	return item, nil
}

func enclosureTypeFromContentType(ct string) podcast.EnclosureType {
	switch ct {
	case "audio/x-m4a", "audio/mp4":
		return podcast.M4A
	case "video/x-m4v":
		return podcast.M4V
	case "video/mp4":
		return podcast.MP4
	case "audio/mpeg", "audio/mp3":
		return podcast.MP3
	case "video/quicktime":
		return podcast.MOV
	case "application/pdf":
		return podcast.PDF
	case "document/x-epub":
		return podcast.EPUB
	default:
		return podcast.MP3
	}
}

func ExtractAudioURL(episode map[string]interface{}) (string, int) {
	duration := 0
	var url string

	if audio, ok := episode["audio"].(map[string]interface{}); ok {
		if u, ok := audio["url"].(string); ok {
			url = u
		}
		if d, ok := audio["duration"].(float64); ok {
			duration = int(d)
		}
	}

	if url == "" {
		if streamMedia, ok := episode["streamMedia"].(map[string]interface{}); ok {
			if u, ok := streamMedia["url"].(string); ok {
				url = u
			}
			if d, ok := streamMedia["duration"].(float64); ok {
				duration = int(d)
			}
			if strings.Contains(url, "hls-media") && strings.Contains(url, "/main.m3u8") {
				url = strings.Replace(url, "hls-media", "audios", 1)
				url = strings.Replace(url, "/main.m3u8", ".mp3", 1)
			}
		}
	}

	return url, duration
}

func URLHeadInfo(ctx context.Context, client *http.Client, id, urlStr string, headers map[string]string, headCache *FileCache, cacheTTL time.Duration) (string, string, error) {
	if entry, ok := headCache.Get(id); ok {
		if m, ok := entry.(map[string]interface{}); ok {
			length, _ := m["length"].(string)
			typ, _ := m["type"].(string)
			if length != "" && typ != "" {
				return length, typ, nil
			}
		}
	}

	retries := 3
	for attempt := 0; attempt < retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, urlStr, nil)
		if err != nil {
			return "0", "audio/mpeg", err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < retries-1 {
				select {
				case <-ctx.Done():
					return "0", "audio/mpeg", ctx.Err()
				case <-time.After(1 * time.Second):
				}
				continue
			}
			return "0", "audio/mpeg", err
		}

		contentLength := "0"
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			contentLength = cl
		}

		contentType := "audio/mpeg"
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			contentType = ct
		}

		resp.Body.Close()

		headCache.Set(id, map[string]interface{}{"length": contentLength, "type": contentType}, cacheTTL)
		return contentLength, contentType, nil
	}

	return "0", "audio/mpeg", fmt.Errorf("all retries failed for HEAD %s", urlStr)
}

func chunks(items []interface{}, n int) [][]interface{} {
	var out [][]interface{}
	for i := 0; i < len(items); i += n {
		end := i + n
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[i:end])
	}
	return out
}
