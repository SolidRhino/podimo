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

func PodcastsToRss(ctx context.Context, podcastID string, data *PodcastData, locale string, headCache *FileCache, publicFeeds bool, headCacheTTL time.Duration, httpClient *http.Client, logger *slog.Logger) ([]byte, error) {
	if data == nil {
		data = &PodcastData{}
	}

	title := GetPodcastName(data)
	if title == "Unknown" && len(data.Episodes) > 0 {
		title = data.Episodes[0].PodcastName
	}
	if title == "" {
		title = "Unknown"
	}

	description := data.Podcast.Description
	if description == "" {
		description = title
	}

	p := podcast.New(title, fmt.Sprintf("https://podimo.com/shows/%s", podcastID), description, nil, nil)

	coverImage := data.Podcast.Images.CoverImageURL
	if coverImage == "" && len(data.Episodes) > 0 {
		coverImage = data.Episodes[0].ImageURL
	}
	if coverImage != "" {
		p.AddImage(coverImage)
	}

	language := data.Podcast.Language
	if language == "" {
		language = locale
	}
	p.Language = language

	author := data.Podcast.AuthorName
	if author == "" && len(data.Episodes) > 0 {
		author = data.Episodes[0].Artist
	}
	if author != "" {
		p.AddAuthor(author, "")
	}

	if !publicFeeds {
		p.IBlock = "yes"
	}

	// Process episodes in chunks of 5 with parallel HEAD requests
	for _, chunk := range chunkEpisodes(data.Episodes, 5) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		items := make([]podcast.Item, len(chunk))
		var wg sync.WaitGroup
		for i, ep := range chunk {
			wg.Add(1)
			go func(idx int, episode Episode) {
				defer wg.Done()
				if ctx.Err() != nil {
					return
				}
				item, err := buildFeedItem(ctx, episode, locale, headCache, headCacheTTL, httpClient, logger)
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

func buildFeedItem(ctx context.Context, episode Episode, locale string, headCache *FileCache, headCacheTTL time.Duration, httpClient *http.Client, logger *slog.Logger) (podcast.Item, error) {
	var item podcast.Item

	url, duration := ExtractAudioURL(episode)
	if url == "" {
		return item, fmt.Errorf("no audio URL")
	}

	item.Title = episode.Title
	item.Description = episode.Description

	if episode.ImageURL != "" {
		item.AddImage(episode.ImageURL)
	}

	pubDateStr := episode.PublishDatetime
	if pubDateStr == "" {
		pubDateStr = episode.Datetime
	}
	if pubDateStr != "" {
		if t, err := time.Parse(time.RFC3339, pubDateStr); err == nil {
			item.AddPubDate(&t)
		}
	}

	item.AddDuration(int64(duration))

	item.GUID = episode.ID

	headers := map[string]string{
		"user-os":        "android",
		"user-agent":     "Podimo/2.45.1 build 566/Android 33",
		"user-version":   "2.45.1",
		"user-locale":    locale,
		"user-unique-id": RandomHexID(16),
	}

	headCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	contentLength, contentType, err := URLHeadInfo(headCtx, httpClient, episode.ID, url, headers, headCache, headCacheTTL, logger)
	if err != nil && logger != nil {
		logger.Debug("HEAD request failed, using safe defaults", "episode_id", episode.ID, "error", err)
	}

	lengthInt, _ := strconv.ParseInt(contentLength, 10, 64)
	if lengthInt < 0 {
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

func ExtractAudioURL(episode Episode) (string, int) {
	if episode.Audio.URL != "" {
		return episode.Audio.URL, int(episode.Audio.Duration)
	}

	url := episode.StreamMedia.URL
	duration := int(episode.StreamMedia.Duration)
	if url == "" {
		return "", 0
	}

	if strings.Contains(url, "hls-media") && strings.Contains(url, "/main.m3u8") {
		url = strings.Replace(url, "hls-media", "audios", 1)
		url = strings.Replace(url, "/main.m3u8", ".mp3", 1)
	}

	return url, duration
}

func URLHeadInfo(ctx context.Context, client *http.Client, id, urlStr string, headers map[string]string, headCache *FileCache, cacheTTL time.Duration, logger *slog.Logger) (string, string, error) {
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

		// Close the body on every path so non-2xx responses never leak.
		resp.Body.Close()

		// Reject non-2xx before reading/caching headers: a 404/500/redirect body
		// must not poison the cache with bogus content-length/type.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt < retries-1 && (resp.StatusCode == 429 || resp.StatusCode >= 500) {
				select {
				case <-ctx.Done():
					return "0", "audio/mpeg", ctx.Err()
				case <-time.After(1 * time.Second):
				}
				continue
			}
			return "0", "audio/mpeg", fmt.Errorf("HEAD %s: status %d", urlStr, resp.StatusCode)
		}

		contentLength := "0"
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			contentLength = cl
		}

		contentType := "audio/mpeg"
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			contentType = ct
		}

		if err := headCache.Set(id, map[string]interface{}{"length": contentLength, "type": contentType}, cacheTTL); err != nil && logger != nil {
			logger.Warn("Failed to cache HEAD info", "episode_id", id, "error", err)
		}
		return contentLength, contentType, nil
	}

	return "0", "audio/mpeg", fmt.Errorf("all retries failed for HEAD %s", urlStr)
}

func chunkEpisodes(items []Episode, n int) [][]Episode {
	var out [][]Episode
	for i := 0; i < len(items); i += n {
		end := i + n
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[i:end])
	}
	return out
}
