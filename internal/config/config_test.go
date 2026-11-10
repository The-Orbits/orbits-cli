package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate points the config at a fresh directory and keeps tokens out of the keychain.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ORBITS_CONFIG_DIR", dir)
	t.Setenv("ORBITS_NO_KEYRING", "1")
	return dir
}

func TestDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	for name, c := range map[string]struct {
		configDir, xdg string
		want           string
	}{
		"ORBITS_CONFIG_DIR wins": {"/tmp/o", "/tmp/x", "/tmp/o"},
		"XDG_CONFIG_HOME next":   {"", "/tmp/x", filepath.Join("/tmp/x", "orbits")},
		"else ~/.config/orbits":  {"", "", filepath.Join(home, ".config", "orbits")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ORBITS_CONFIG_DIR", c.configDir)
			t.Setenv("XDG_CONFIG_HOME", c.xdg)
			got, err := Dir()
			if err != nil || got != c.want {
				t.Fatalf("Dir() = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestLoadWithoutAFileIsEmpty(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Current != "" || cfg.Profiles == nil || len(cfg.Profiles) != 0 {
		t.Fatalf("cfg = %+v, want empty with a usable map", cfg)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := isolate(t)
	cfg := &Config{Current: "work", Profiles: map[string]Profile{
		"work": {TeamID: "team_1", TeamSlug: "acme", TeamName: "Acme", TokenID: "tok_1", Access: "full"},
		"home": {TeamID: "team_2"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config.json mode = %v, want 0600", info.Mode().Perm())
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("Load() = %+v, want %+v", got, cfg)
	}
	if names := got.Names(); !reflect.DeepEqual(names, []string{"home", "work"}) {
		t.Fatalf("Names() = %v", names)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	dir := isolate(t)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("broken JSON was accepted")
	}
}

func TestName(t *testing.T) {
	c := &Config{}
	if c.Name("") != DefaultProfile {
		t.Fatalf("no current: %q", c.Name(""))
	}
	c.Current = "work"
	if c.Name("") != "work" || c.Name("home") != "home" {
		t.Fatalf("Name: %q %q", c.Name(""), c.Name("home"))
	}
}

func TestProfileTeam(t *testing.T) {
	if (Profile{TeamID: "team_1", TeamSlug: "acme"}).Team() != "acme" {
		t.Fatal("slug first")
	}
	if (Profile{TeamID: "team_1"}).Team() != "team_1" {
		t.Fatal("else the id")
	}
}

func TestTokenFileWithoutKeychain(t *testing.T) {
	dir := isolate(t)
	where, err := SaveToken("work", "orb_live_one")
	if err != nil || where != InFile {
		t.Fatalf("SaveToken = %q, %v", where, err)
	}
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json: %v, %v", info, err)
	}
	if _, err := SaveToken("home", "orb_live_two"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := LoadToken("work"); tok != "orb_live_one" {
		t.Fatalf("work token = %q", tok)
	}
	if err := DeleteToken("work"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := LoadToken("work"); tok != "" {
		t.Fatalf("deleted token still there: %q", tok)
	}
	if tok, _ := LoadToken("home"); tok != "orb_live_two" {
		t.Fatalf("deleting one profile's token touched another's: %q", tok)
	}
	if err := DeleteToken("home"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an empty credentials.json should be removed: %v", err)
	}
}

// fakeKeychain stands in for the OS keychain.
type fakeKeychain struct {
	items  map[string]string
	broken bool
}

var errNoKeychain = errors.New("no keychain")

func (f *fakeKeychain) Set(s, u, p string) error {
	if f.broken {
		return errNoKeychain
	}
	f.items[s+"/"+u] = p
	return nil
}

func (f *fakeKeychain) Get(s, u string) (string, error) {
	if f.broken {
		return "", errNoKeychain
	}
	v, ok := f.items[s+"/"+u]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *fakeKeychain) Delete(s, u string) error {
	if f.broken {
		return errNoKeychain
	}
	delete(f.items, s+"/"+u)
	return nil
}

func useKeychain(t *testing.T, k *fakeKeychain) {
	t.Helper()
	old := keychain
	keychain = k
	t.Cleanup(func() { keychain = old })
}

func TestTokenInKeychain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORBITS_CONFIG_DIR", dir)
	t.Setenv("ORBITS_NO_KEYRING", "")
	k := &fakeKeychain{items: map[string]string{}}
	useKeychain(t, k)

	// A token in the file from before moves to the keychain on the next save.
	if err := setFileToken("work", "orb_live_old"); err != nil {
		t.Fatal(err)
	}
	where, err := SaveToken("work", "orb_live_new")
	if err != nil || where != InKeychain {
		t.Fatalf("SaveToken = %q, %v", where, err)
	}
	if k.items[KeyringService+"/work"] != "orb_live_new" {
		t.Fatalf("keychain = %v", k.items)
	}
	if tok, _ := fileToken("work"); tok != "" {
		t.Fatalf("the old token lingered in the file: %q", tok)
	}
	if tok, _ := LoadToken("work"); tok != "orb_live_new" {
		t.Fatalf("LoadToken = %q", tok)
	}
	if err := DeleteToken("work"); err != nil {
		t.Fatal(err)
	}
	if len(k.items) != 0 {
		t.Fatalf("DeleteToken left %v", k.items)
	}
}

// No keychain on the machine (a server without a Secret Service): the file
// takes the token, and reading falls back to it.
func TestKeychainUnavailableFallsBackToTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORBITS_CONFIG_DIR", dir)
	t.Setenv("ORBITS_NO_KEYRING", "")
	useKeychain(t, &fakeKeychain{broken: true})
	where, err := SaveToken("work", "orb_live_x")
	if err != nil || where != InFile {
		t.Fatalf("SaveToken = %q, %v", where, err)
	}
	if tok, _ := LoadToken("work"); tok != "orb_live_x" {
		t.Fatalf("LoadToken = %q", tok)
	}
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json: %v, %v", info, err)
	}
	if err := DeleteToken("work"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := LoadToken("work"); tok != "" {
		t.Fatalf("token still there: %q", tok)
	}
}
