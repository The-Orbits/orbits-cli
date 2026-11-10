// Package e2e runs the real orbits binary against a real team, on staging
// by default. It runs only with ORBITS_E2E_TOKEN and ORBITS_E2E_TEAM set:
//
//	ORBITS_E2E_TOKEN=orb_live_… ORBITS_E2E_TEAM=<team> go test -v ./e2e
//
// ORBITS_E2E_API_URL picks another API (default: staging). The test makes
// its own upload project and deletes it at the end; it needs a full-access
// token for a team that may create projects.
package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const stagingAPI = "https://stagingapi.theorbits.app/v1"

type cli struct {
	t   *testing.T
	bin string
	dir string
	env []string
}

// run runs orbits in the test's folder and returns its stdout; a failure
// (or a success, when it should fail) ends the test.
func (c *cli) run(stdin string, args ...string) string {
	c.t.Helper()
	out, errOut, err := c.try(stdin, args...)
	if err != nil {
		c.t.Fatalf("orbits %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

func (c *cli) try(stdin string, args ...string) (string, string, error) {
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.dir
	cmd.Env = c.env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	c.t.Logf("orbits %s\n%s", strings.Join(args, " "), errOut.String())
	return out.String(), errOut.String(), err
}

func TestLive(t *testing.T) {
	token, team := os.Getenv("ORBITS_E2E_TOKEN"), os.Getenv("ORBITS_E2E_TEAM")
	if token == "" || team == "" {
		t.Skip("set ORBITS_E2E_TOKEN and ORBITS_E2E_TEAM to run the live test")
	}
	apiURL := os.Getenv("ORBITS_E2E_API_URL")
	if apiURL == "" {
		apiURL = stagingAPI
	}

	bin := filepath.Join(t.TempDir(), "orbits")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/orbits")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<!doctype html><title>orbits-cli e2e</title><h1>orbits-cli e2e</h1>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &cli{t: t, bin: bin, dir: site, env: append(os.Environ(),
		"ORBITS_TOKEN="+token,
		"ORBITS_API_URL="+apiURL,
		"ORBITS_TEAM=",
		"ORBITS_CONFIG_DIR="+t.TempDir(),
		"ORBITS_NO_KEYRING=1",
		"ORBITS_NO_UPDATE_NOTIFIER=1",
		"NO_COLOR=1",
	)}

	// Link: a new upload project, named so a leftover is easy to spot.
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "cli-e2e-" + hex.EncodeToString(suffix)
	var project struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	out := c.run("", "--team", team, "link", "--create", name, "--json")
	if err := json.Unmarshal([]byte(out), &project); err != nil || project.ID == "" {
		t.Fatalf("link --json: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if _, errOut, err := c.try("", "projects", "delete", project.ID, "--yes"); err != nil {
			t.Errorf("deleting %s (%s) failed: delete it by hand: %v\n%s", name, project.ID, err, errOut)
		}
	})
	if _, err := os.Stat(filepath.Join(site, ".orbits", "project.json")); err != nil {
		t.Fatalf("no link file: %v", err)
	}

	// Deploy the folder; stdout is the address.
	url := strings.TrimSpace(c.run("", "deploy", "--no-logs"))
	if !strings.HasPrefix(url, "https://") {
		t.Fatalf("deploy printed %q, want an address", url)
	}

	// Its build log.
	if logs := c.run("", "logs"); strings.TrimSpace(logs) == "" {
		t.Fatal("the build log is empty")
	}
	if _, _, err := c.try("", "deployments", "list", "--json"); err != nil {
		t.Fatalf("deployments list: %v", err)
	}

	// A variable, and pulling it back.
	value := "hello from orbits-cli " + time.Now().UTC().Format(time.RFC3339)
	c.run(value, "env", "add", "E2E_GREETING", "--plain", "--environment", "development")
	if ls := c.run("", "env", "ls"); !strings.Contains(ls, "E2E_GREETING") || strings.Contains(ls, value) {
		t.Fatalf("env ls: %s", ls)
	}
	c.run("", "env", "pull", "--yes")
	pulled := filepath.Join(site, ".env.local")
	info, err := os.Stat(pulled)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf(".env.local mode %v", info.Mode().Perm())
	}
	data, _ := os.ReadFile(pulled)
	if !strings.Contains(string(data), `E2E_GREETING="`+value+`"`) {
		t.Fatalf(".env.local:\n%s", data)
	}
	c.run("", "env", "rm", "E2E_GREETING", "--yes")
}
