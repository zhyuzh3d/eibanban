package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

func loadConfig() Config {
	home, _ := os.UserHomeDir()
	cfg := Config{
		BaseURL: "https://ai.martin98.top/v1",
		Model:   "gpt-5.5",
		APIKey:  os.Getenv("OPENAI_API_KEY"),
	}
	if b, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
		if s := tomlString(string(b), "base_url"); s != "" {
			cfg.BaseURL = s
		}
		if s := tomlString(string(b), "model"); s != "" {
			cfg.Model = s
		}
	}
	if cfg.APIKey == "" {
		if b, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json")); err == nil {
			var auth map[string]string
			if json.Unmarshal(b, &auth) == nil {
				cfg.APIKey = auth["OPENAI_API_KEY"]
			}
		}
	}
	return cfg
}

func tomlString(src, key string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]+)"`)
	m := re.FindStringSubmatch(src)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}
