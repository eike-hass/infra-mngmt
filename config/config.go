package config

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ProcessCompose struct {
	Name        string `yaml:"name"`
	Endpoint    string `yaml:"endpoint"`
	Binary      string `yaml:"binary"`
	ComposeFile string `yaml:"compose_file"`
	Token       string `yaml:"token,omitempty"`      // process-compose API token; takes precedence over TokenFile
	TokenFile   string `yaml:"token_file,omitempty"` // path to file containing the token; same file is passed to process-compose via --token-file
}

// LlamaServer declares an llama.cpp `llama-server` HTTP endpoint that the UI
// can probe for live stats (`/health`, `/props`, `/metrics`, `/slots`). The
// pair (Instance, Process) is matched against process-compose process rows so
// the "llama" button only renders next to processes we can actually probe.
//
// `wsl-windows` in Endpoint is resolved at runtime to the Windows host IP via
// the same path as ProcessCompose endpoints (see ResolveEndpoint / wsl.go).
type LlamaServer struct {
	Instance   string `yaml:"instance"`          // process-compose instance name
	Process    string `yaml:"process"`           // process-compose process name
	Endpoint   string `yaml:"endpoint"`          // base URL, e.g. "http://wsl-windows:8080"
	APIKey     string `yaml:"api_key,omitempty"` // bearer token; takes precedence over APIKeyFile
	APIKeyFile string `yaml:"api_key_file,omitempty"`
}

type Config struct {
	Bind             string           `yaml:"bind"`
	TokenFile        string           `yaml:"token_file"`
	ProcessCompose   []ProcessCompose `yaml:"process_compose"`
	LlamaServers     []LlamaServer    `yaml:"llama_servers,omitempty"`
	BridgesFile      string           `yaml:"bridges_file,omitempty"`
	DependenciesFile string           `yaml:"dependencies_file,omitempty"`
	ContainersFile   string           `yaml:"containers_file,omitempty"`
	// ModelRatesFile points to an optional per-model token-pricing YAML
	// (USD per million tokens) used by the Open Design card's blended-cost
	// approximation. When unset, falls back to model-rates.yaml alongside
	// config.yaml. Missing file is fine — every cost slot renders `—`.
	ModelRatesFile string `yaml:"model_rates_file,omitempty"`
	// BridgesComposeFile is the path to the generated process-compose fragment
	// that owns long-running socat relays for tier=wsl bridges. The user's
	// main process-compose.yaml references it via `extends:` so the fragment
	// gets merged at PC startup. infra-mngmt rewrites this file on every
	// `bridges apply` and tells the WSL process-compose to reload.
	BridgesComposeFile string `yaml:"bridges_compose_file,omitempty"`
	// TrustedNetworks lists CIDRs whose connections bypass the bearer-token
	// auth check. Use this to skip the login flow for local access (loopback,
	// Docker bridge, WSL adapter) while still requiring auth for everything
	// else. Empty (default) means every request is authenticated when
	// token_file is set. Loopback is NOT trusted by default — add explicitly
	// if you want browser-localhost to skip login.
	TrustedNetworks []string `yaml:"trusted_networks,omitempty"`
	// WakeURL is the browser-facing URL of the Windows-side wake-proxy that
	// triggers `wsl.exe -- /bin/true` to bring WSL out of vmIdleTimeout shutdown.
	// When set, the page renders a status pill and a JS island that pings
	// /api/version while the tab is visible, firing the wake fetch on failure.
	// Empty → feature disabled, no pill, no SW registration, no CSP extension.
	// Example: "http://localhost:9920/process/start/wsl-wake".
	WakeURL string `yaml:"wake_url,omitempty"`
	// Explicit project root paths to add (each must contain a .claude/ subdir).
	ExtraPaths []string `yaml:"extra_paths"`
}

// DefaultPath returns the canonical config path.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "infra-mngmt", "config.yaml")
}

func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Bind:               "127.0.0.1:7842",
		TokenFile:          filepath.Join(home, ".config", "infra-mngmt", "token"),
		BridgesComposeFile: filepath.Join(home, ".config", "infra-mngmt", "process-compose.bridges.yaml"),
	}
}

// Load reads and parses a YAML config file. Missing file returns the default
// config — callers that need to distinguish "fresh install" from "user
// removed config" should stat the path themselves first.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks structural invariants that yaml.Unmarshal cannot — most
// importantly that each process_compose entry has the fields the runtime
// relies on. A typo'd top-level key (e.g. `process_compos`) is silently
// dropped by yaml.Unmarshal, so without this check a broken config surfaces
// only as an empty services panel rather than a startup error.
func (c *Config) Validate() error {
	for i, pc := range c.ProcessCompose {
		if pc.Name == "" {
			return fmt.Errorf("process_compose[%d]: name is required", i)
		}
		if pc.Endpoint == "" {
			return fmt.Errorf("process_compose[%d] (%q): endpoint is required", i, pc.Name)
		}
	}
	return nil
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

// Save writes the config as YAML.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
