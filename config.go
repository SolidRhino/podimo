package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	Hostname          string `mapstructure:"hostname"`
	BindHost          string `mapstructure:"bind_host"`
	Protocol          string `mapstructure:"protocol"`
	HTTPProxy         string `mapstructure:"http_proxy"`
	ZenRowsAPI        string `mapstructure:"zenrows_api"`
	ScraperAPI        string `mapstructure:"scraper_api"`
	CacheDir          string `mapstructure:"cache_dir"`
	BlockListFile     string `mapstructure:"block_list_file"`
	Debug             bool   `mapstructure:"debug"`
	LocalCredentials  bool   `mapstructure:"local_credentials"`
	Email             string `mapstructure:"email"`
	Password          string `mapstructure:"password"`
	GraphQLURL        string `mapstructure:"graphql_url"`
	StoreTokensOnDisk bool   `mapstructure:"store_tokens_on_disk"`
	TokenCacheTime    time.Duration `mapstructure:"token_cache_time"`
	PodcastCacheTime  time.Duration `mapstructure:"podcast_cache_time"`
	HeadCacheTime     time.Duration `mapstructure:"head_cache_time"`
	PublicFeeds       bool   `mapstructure:"public_feeds"`
	VideoEnabled      bool   `mapstructure:"video_enabled"`
	VideoCheckEnabled bool   `mapstructure:"video_check_enabled"`
	VideoTitleSuffix  string `mapstructure:"video_title_suffix"`
	Locales           []string `mapstructure:"-"`
	Regions           []Region `mapstructure:"-"`
	Blocked           map[string]struct{} `mapstructure:"-"`
}

type Region struct {
	Code string
	Name string
}

func LoadConfig(configFile string) (*Config, error) {
	_ = godotenv.Load(".env")

	v := viper.New()
	v.SetEnvPrefix("PODIMO")
	v.AutomaticEnv()

	// Defaults
	v.SetDefault("hostname", "localhost:12104")
	v.SetDefault("bind_host", "127.0.0.1:12104")
	v.SetDefault("protocol", "http")
	v.SetDefault("cache_dir", "./cache")
	v.SetDefault("block_list_file", "./.block-list")
	v.SetDefault("debug", false)
	v.SetDefault("local_credentials", false)
	v.SetDefault("graphql_url", "https://podimo.com/graphql")
	v.SetDefault("store_tokens_on_disk", true)
	v.SetDefault("token_cache_time", 5*24*time.Hour)
	v.SetDefault("podcast_cache_time", 6*time.Hour)
	v.SetDefault("head_cache_time", 7*24*time.Hour)
	v.SetDefault("public_feeds", false)
	v.SetDefault("video_enabled", false)
	v.SetDefault("video_check_enabled", false)

	if configFile != "" {
		v.SetConfigFile(configFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config file %q: %w", configFile, err)
		}
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("/etc/podimo-rss/")
		v.AddConfigPath(".")
		_ = v.ReadInConfig() // optional: no file is okay
	}

	cfg := &Config{
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

	hook := mapstructure.ComposeDecodeHookFunc(
		strictBoolHook,
		strictDurationHook,
	)

	if err := v.Unmarshal(cfg, func(dc *mapstructure.DecoderConfig) {
		dc.DecodeHook = hook
		// Preserve zero values where appropriate
		dc.ZeroFields = false
	}); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
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

func strictBoolHook(from, to reflect.Type, data interface{}) (interface{}, error) {
	if from.Kind() != reflect.String || to.Kind() != reflect.Bool {
		return data, nil
	}
	return parseBool(data.(string))
}

func strictDurationHook(from, to reflect.Type, data interface{}) (interface{}, error) {
	if from.Kind() != reflect.String || to != reflect.TypeOf(time.Duration(0)) {
		return data, nil
	}
	return parseDuration(data.(string), 0)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseBool(v string) (bool, error) {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "true", "1", "t", "y", "yes":
		return true, nil
	case "false", "0", "f", "n", "no", "":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", v)
	}
}

func parseDuration(v string, fallback time.Duration) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback, nil
	}
	if seconds, err := strconv.Atoi(v); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	return fallback, fmt.Errorf("invalid duration value %q", v)
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
