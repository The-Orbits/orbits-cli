// Package config keeps the CLI's profiles (which team, which token) in
// ~/.config/orbits/config.json, and each profile's API token in the OS
// keychain, or in a file only you can read where there is none.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DefaultProfile is the profile used when none is named or current.
const DefaultProfile = "default"

// Profile is one sign-in: a token, which acts inside one team. The token
// itself is never here; it is in the keychain (or credentials.json).
type Profile struct {
	TeamID   string `json:"team_id,omitempty"`
	TeamSlug string `json:"team_slug,omitempty"`
	TeamName string `json:"team_name,omitempty"`

	TokenID   string `json:"token_id,omitempty"`
	TokenName string `json:"token_name,omitempty"`
	// Access is "full" or "read": as approved in the browser, or as GET /me
	// says for a pasted token (--with-token); "" for a sign-in from before
	// the API said.
	Access    string `json:"access,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`

	// APIURL is the API the token belongs to, with /v1, when it is not the
	// default (a staging sign-in). ORBITS_API_URL still wins.
	APIURL string `json:"api_url,omitempty"`

	UserEmail string `json:"user_email,omitempty"`
}

// Team is how the profile names its team in API paths: the slug, else the id.
func (p Profile) Team() string {
	if p.TeamSlug != "" {
		return p.TeamSlug
	}
	return p.TeamID
}

// Config is every profile on this machine.
type Config struct {
	// Current is the profile used when --profile is not given.
	Current  string             `json:"current,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}

// Dir is $ORBITS_CONFIG_DIR, else $XDG_CONFIG_HOME/orbits, else ~/.config/orbits.
func Dir() (string, error) {
	if d := os.Getenv("ORBITS_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "orbits"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "orbits"), nil
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config, or returns an empty one if there is none yet.
func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	cfg := &Config{Profiles: map[string]Profile{}}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", p, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return cfg, nil
}

// Save writes the config atomically, readable only by you.
func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(p, append(data, '\n'))
}

// Name is the profile to use: the one asked for, else the current one, else "default".
func (c *Config) Name(asked string) string {
	switch {
	case asked != "":
		return asked
	case c.Current != "":
		return c.Current
	default:
		return DefaultProfile
	}
}

// Names lists the profiles, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// writeFile replaces a file atomically, mode 0600.
func writeFile(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
