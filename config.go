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
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	envprovider "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Hostname           string              `koanf:"hostname"`
	BindHost           string              `koanf:"bind_host"`
	Protocol           string              `koanf:"protocol"`
	HTTPProxy          string              `koanf:"http_proxy"`
	ZenRowsAPI         string              `koanf:"zenrows_api"`
	ScraperAPI         string              `koanf:"scraper_api"`
	CacheDir           string              `koanf:"cache_dir"`
	BlockListFile      string              `koanf:"block_list_file"`
	Debug              bool                `koanf:"debug"`
	LocalCredentials   bool                `koanf:"local_credentials"`
	Email              string              `koanf:"email"`
	Password           string              `koanf:"password"`
	GraphQLURL         string              `koanf:"graphql_url"`
	StoreTokensOnDisk  bool                `koanf:"store_tokens_on_disk"`
	TokenCacheTime     time.Duration       `koanf:"token_cache_time"`
	PodcastCacheTime   time.Duration       `koanf:"podcast_cache_time"`
	HeadCacheTime      time.Duration       `koanf:"head_cache_time"`
	PublicFeeds        bool                `koanf:"public_feeds"`
	TrustedProxyHeader string              `koanf:"trusted_proxy_header"`
	Locales            []string            `koanf:"-"`
	Regions            []Region            `koanf:"-"`
	Blocked            map[string]struct{} `koanf:"-"`
}

type Region struct {
	Code string
	Name string
}

func LoadConfig(configFile string) (*Config, error) {
	_ = godotenv.Load(".env")

	k := koanf.New(".")

	// 1. Defaults — loaded first (lowest precedence). Durations are stored as
	//    their .String() form so strictDurationHook handles them uniformly with
	//    file/env string values; bool defaults are native bools (trusted).
	defaults := map[string]any{
		"hostname":             "localhost:12104",
		"bind_host":            "127.0.0.1:12104",
		"protocol":             "http",
		"cache_dir":            "./cache",
		"block_list_file":      "./.block-list",
		"debug":                false,
		"local_credentials":    false,
		"graphql_url":          "https://podimo.com/graphql",
		"store_tokens_on_disk": true,
		"token_cache_time":     (5 * 24 * time.Hour).String(),
		"podcast_cache_time":   (6 * time.Hour).String(),
		"head_cache_time":      (7 * 24 * time.Hour).String(),
		"public_feeds":         false,
		"trusted_proxy_header": "",
	}
	if err := k.Load(confmap.Provider(defaults, ""), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}

	// 2. File — explicit --config path, or search /etc/podimo-rss/ then .
	//    (optional: no file is okay and defaults+env still apply).
	yamlPath := configFile
	if yamlPath == "" {
		for _, candidate := range []string{"/etc/podimo-rss/config.yaml", "./config.yaml"} {
			if _, err := os.Stat(candidate); err == nil {
				yamlPath = candidate
				break
			}
		}
	}
	if yamlPath != "" {
		if err := k.Load(file.Provider(yamlPath), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("read config file %q: %w", yamlPath, err)
		}
	} else if configFile != "" {
		// Explicit --config path that doesn't exist must hard-fail.
		return nil, fmt.Errorf("read config file %q: %w", configFile, os.ErrNotExist)
	}

	// 3. Env — PODIMO_* prefix, strip prefix + lowercase to flat snake_case.
	//    Empty values are skipped to preserve "empty = unset" semantics, so
	//    blanked PODIMO_* vars don't clobber file/default values.
	if err := k.Load(envprovider.Provider("", envprovider.Opt{
		Prefix: "PODIMO_",
		TransformFunc: func(key, val string) (string, any) {
			if val == "" {
				return "", nil // skip empty env vars
			}
			return strings.ToLower(strings.TrimPrefix(key, "PODIMO_")), val
		},
	}), nil); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
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

	// 4. Decode with strict hooks: strictBoolHook rejects invalid booleans,
	//    strictDurationHook rejects invalid durations and accepts bare-integer-seconds.
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				strictBoolHook,
				strictDurationHook,
			),
			ZeroFields: false,
		},
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
		defer func() { _ = f.Close() }()
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
