package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Hostname          string
	BindHost          string
	Protocol          string
	HTTPProxy         string
	ZenRowsAPI        string
	ScraperAPI        string
	CacheDir          string
	BlockListFile     string
	Debug             bool
	LocalCredentials  bool
	Email             string
	Password          string
	GraphQLURL        string
	StoreTokensOnDisk bool
	TokenCacheTime    time.Duration
	PodcastCacheTime  time.Duration
	HeadCacheTime     time.Duration
	PublicFeeds       bool
	VideoEnabled      bool
	VideoCheckEnabled bool
	VideoTitleSuffix  string
	Locales           []string
	Regions           []Region
	Blocked           map[string]struct{}
}

type Region struct {
	Code string
	Name string
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load(".env")

	cfg := &Config{
		Hostname:          getEnv("PODIMO_HOSTNAME", "localhost:12104"),
		BindHost:          getEnv("PODIMO_BIND_HOST", "127.0.0.1:12104"),
		Protocol:          getEnv("PODIMO_PROTOCOL", "http"),
		HTTPProxy:         os.Getenv("HTTP_PROXY"),
		ZenRowsAPI:        os.Getenv("ZENROWS_API"),
		ScraperAPI:        os.Getenv("SCRAPER_API"),
		CacheDir:          getEnv("CACHE_DIR", "./cache"),
		BlockListFile:     getEnv("BLOCK_LIST_FILE", "./.block-list"),
		Debug:             parseBool(os.Getenv("DEBUG")),
		LocalCredentials:  parseBool(os.Getenv("LOCAL_CREDENTIALS")),
		Email:             os.Getenv("PODIMO_EMAIL"),
		Password:          os.Getenv("PODIMO_PASSWORD"),
		GraphQLURL:        "https://podimo.com/graphql",
		StoreTokensOnDisk: parseBool(getEnv("STORE_TOKENS_ON_DISK", "true")),
		TokenCacheTime:    parseDuration(os.Getenv("TOKEN_CACHE_TIME"), 5*24*time.Hour),
		PodcastCacheTime:  parseDuration(os.Getenv("PODCAST_CACHE_TIME"), 6*time.Hour),
		HeadCacheTime:     parseDuration(os.Getenv("HEAD_CACHE_TIME"), 7*24*time.Hour),
		PublicFeeds:       parseBool(os.Getenv("PUBLIC_FEEDS")),
		VideoEnabled:      parseBool(os.Getenv("ENABLE_VIDEO")),
		VideoCheckEnabled: parseBool(os.Getenv("ENABLE_VIDEO_CHECK")),
		VideoTitleSuffix:  os.Getenv("VIDEO_TITLE_SUFFIX"),
		Locales: []string{
			"nl-NL", "de-DE", "da-DK", "es-ES", "en-US",
			"es-MX", "no-NO", "fi-FI", "en-GB",
		},
		Regions: []Region{
			{Code: "nl", Name: "Nederland"},
			{Code: "de", Name: "Deutschland"},
			{Code: "dk", Name: "Danmark"},
			{Code: "es", Name: "España"},
			{Code: "latam", Name: "America latina"},
			{Code: "en", Name: "International"},
			{Code: "mx", Name: "Mexico"},
			{Code: "no", Name: "Norge"},
			{Code: "fi", Name: "Suomi"},
			{Code: "uk", Name: "United Kingdom"},
		},
		Blocked: make(map[string]struct{}),
	}

	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	absDir, err := filepath.Abs(cfg.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("resolve cache dir: %w", err)
	}
	cfg.CacheDir = absDir

	if _, err := os.Stat(cfg.BlockListFile); err == nil {
		f, err := os.Open(cfg.BlockListFile)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.Fields(line)[0]
			cfg.Blocked[line] = struct{}{}
		}
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "true", "1", "t", "y", "yes":
		return true
	default:
		return false
	}
}

func parseDuration(v string, fallback time.Duration) time.Duration {
	if v == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(v); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func (c *Config) isValidRegion(region string) bool {
	for _, r := range c.Regions {
		if r.Code == region {
			return true
		}
	}
	return false
}

func (c *Config) isValidLocale(locale string) bool {
	for _, l := range c.Locales {
		if l == locale {
			return true
		}
	}
	return false
}
