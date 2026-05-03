package config

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProcessCompose struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Binary      string `json:"binary"`
	ComposeFile string `json:"compose_file"`
	Token       string `json:"token,omitempty"` // bearer token for process-compose auth
}

type Config struct {
	Bind           string           `json:"bind"`
	TokenFile      string           `json:"token_file"`
	ProcessCompose []ProcessCompose `json:"process_compose"`
	// Explicit project root paths to add (each must contain a .claude/ subdir).
	ExtraPaths []string `json:"extra_paths"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "infra-mngmt", "config.json")
}

func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Bind:      "127.0.0.1:7842",
		TokenFile: filepath.Join(home, ".config", "infra-mngmt", "token"),
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// LoadOrCreateToken reads the bearer token from path. If the file does not
// exist a new 64-hex-character token is generated, written to path with 0600
// permissions, and returned. Returns ("", nil) when path is empty (auth
// disabled by explicit opt-out).
func LoadOrCreateToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t, nil
		}
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("read token file: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := crand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write token file: %w", err)
	}
	return token, nil
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
