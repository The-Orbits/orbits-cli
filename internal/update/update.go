// Package update finds newer releases of the CLI on GitHub and installs them
// over the running binary, from the same archives install.sh downloads.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases are published.
const Repo = "The-Orbits/orbits-cli"

// BaseURL is the repository's releases: <BaseURL>/latest redirects to the
// newest release's tag, and <BaseURL>/download/<tag>/ holds
// orbits_<os>_<arch>.tar.gz and checksums.txt. A variable so tests can point
// it elsewhere.
var BaseURL = "https://github.com/" + Repo + "/releases"

// ChecksumsFile is the release's list of SHA-256 sums.
const ChecksumsFile = "checksums.txt"

var httpClient = &http.Client{Timeout: 2 * time.Minute}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 200<<20))
}

// Latest is the newest release's tag: the one GitHub's /releases/latest
// redirects to (pre-releases are never "latest").
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("finding the latest version: %w", err)
	}
	_ = res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode < 300 || res.StatusCode > 399 || loc == "" {
		return "", fmt.Errorf("finding the latest version: %s answered %s", BaseURL+"/latest", res.Status)
	}
	v := path.Base(strings.TrimRight(loc, "/"))
	if _, ok := parse(v); !ok {
		return "", fmt.Errorf("finding the latest version: %q is not a version", v)
	}
	return v, nil
}

// Newer reports whether latest is a later release than current. A build
// that is not a release ("dev", a commit) is never offered one.
func Newer(current, latest string) bool {
	if !IsRelease(current) {
		return false
	}
	c, _ := parse(current)
	l, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// IsRelease reports whether v is a release's version, vMAJOR.MINOR.PATCH.
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok && !strings.Contains(v, "-g")
}

// parse reads vMAJOR.MINOR.PATCH, ignoring any -suffix.
func parse(v string) ([3]int, bool) {
	var out [3]int
	core, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(core, ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Platform is this machine's, as the archives name it: darwin_arm64.
func Platform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// Archive is the release archive for this machine.
func Archive() string { return "orbits_" + Platform() + ".tar.gz" }

// Install downloads version, checks it against the release's checksums.txt
// and replaces the binary at exe with it.
func Install(ctx context.Context, version, exe string) error {
	archive := Archive()
	base := BaseURL + "/download/" + version + "/"
	sums, err := get(ctx, base+ChecksumsFile)
	if err != nil {
		return fmt.Errorf("no %s release: %w", version, err)
	}
	want := checksum(sums, archive)
	if want == "" {
		return fmt.Errorf("%s has no build for %s", version, Platform())
	}
	body, err := get(ctx, base+archive)
	if err != nil {
		return err
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s does not match its checksum: not installing it", archive)
	}
	bin, err := extract(body)
	if err != nil {
		return fmt.Errorf("%s: %w", archive, err)
	}
	return replace(exe, bin)
}

func checksum(sums []byte, name string) string {
	s := bufio.NewScanner(bytes.NewReader(sums))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

func extract(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no orbits binary in it")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "orbits" {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// replace writes bin next to exe and renames it over exe, so a failure
// leaves the old binary in place.
func replace(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".orbits-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cannot write to %s: %w", dir, ErrNotWritable)
		}
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), exe)
}

// ErrNotWritable is a binary in a folder the CLI may not write.
var ErrNotWritable = errors.New("not writable")

// NPMPackage is the npm package the CLI is published as. It installs the
// binary from one of its platform packages, @useorbits/cli-<os>-<arch>.
const NPMPackage = "@useorbits/cli"

// The package managers that install the CLI and upgrade it themselves.
const (
	Homebrew = "homebrew"
	NPM      = "npm"
)

// Manager reports which package manager installed exe: Homebrew, NPM (npm,
// pnpm, Yarn or Bun, which all leave it in a node_modules folder), or "" for
// install.sh or a downloaded archive, which "orbits update" replaces in place.
func Manager(exe string) string {
	exe = filepath.ToSlash(exe)
	switch {
	case strings.Contains(exe, "/node_modules/"+NPMPackage+"-"):
		return NPM
	case strings.Contains(exe, "/Caskroom/") || strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/homebrew/"):
		return Homebrew
	}
	return ""
}

// UpgradeCommand is what upgrades a CLI that manager installed.
func UpgradeCommand(manager string) string {
	switch manager {
	case Homebrew:
		return "brew upgrade orbits"
	case NPM:
		return "npm install -g " + NPMPackage + "@latest"
	}
	return "orbits update"
}

// State is the last check for a newer version, kept between runs so the
// network is asked at most once a day.
type State struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// CheckInterval is how often the network is asked.
const CheckInterval = 24 * time.Hour

// LoadState reads the last check, or the zero State.
func LoadState(path string) State {
	var s State
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// SaveState writes the state at path, quietly: a failure only means asking
// again next time.
func SaveState(path string, s State) {
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
