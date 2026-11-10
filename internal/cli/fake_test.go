package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/The-Orbits/orbits-cli/internal/config"
	"github.com/The-Orbits/orbits-cli/internal/link"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

// testToken is the token every fake API call must carry.
const testToken = "orb_live_testtesttesttesttesttesttest"

// call is one request the fake API received.
type call struct {
	Method, Path, Query, IdempotencyKey, UserAgent string
	Header                                         http.Header
	Body                                           []byte
}

// reply is one canned answer; a route with several answers gives them in
// turn and then repeats the last, the way polling sees a status change.
type reply struct {
	status int
	body   string
}

// fakeAPI stands in for the Orbits API: routes are "METHOD /path" without
// the /v1 prefix. A body starting with "event:" or "id:" is served as
// server-sent events. Paths under /storage/ are the presigned upload target
// and never need the token.
type fakeAPI struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string][]reply
	served map[string]int
	calls  []call
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	f := &fakeAPI{t: t, routes: map[string][]reply{}, served: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAPI) on(route string, status int, body string) *fakeAPI {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[route] = append(f.routes[route], reply{status, body})
	return f
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	f.mu.Lock()
	f.calls = append(f.calls, call{r.Method, path, r.URL.RawQuery, r.Header.Get("Idempotency-Key"), r.Header.Get("User-Agent"), r.Header.Clone(), body})
	key := r.Method + " " + path
	replies, ok := f.routes[key]
	i := f.served[key]
	f.served[key]++
	f.mu.Unlock()
	if !strings.HasPrefix(path, "/storage/") {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			f.t.Errorf("%s: Authorization = %q", key, r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodPost && r.Header.Get("Idempotency-Key") == "" {
			f.t.Errorf("%s: a POST without an Idempotency-Key", key)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "orbits-cli/") {
			f.t.Errorf("%s: User-Agent = %q", key, r.Header.Get("User-Agent"))
		}
	} else if r.Header.Get("Authorization") != "" {
		f.t.Errorf("%s: the token was sent to storage", key)
	}
	if !ok {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"title":"Not Found","detail":"no fake route for ` + key + `"}`))
		return
	}
	rep := replies[min(i, len(replies)-1)]
	switch {
	case strings.HasPrefix(rep.body, "event:") || strings.HasPrefix(rep.body, "id:"):
		w.Header().Set("Content-Type", "text/event-stream")
	case rep.status >= 400:
		w.Header().Set("Content-Type", "application/problem+json")
	default:
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(rep.status)
	_, _ = w.Write([]byte(rep.body))
}

// sent returns the requests made to a route.
func (f *fakeAPI) sent(method, path string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// writes is every request that is not a GET.
func (f *fakeAPI) writes() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

// sentJSON decodes the one body sent to a route.
func (f *fakeAPI) sentJSON(method, path string) map[string]any {
	f.t.Helper()
	calls := f.sent(method, path)
	if len(calls) != 1 {
		f.t.Fatalf("%s %s: %d requests, want 1", method, path, len(calls))
	}
	var m map[string]any
	if err := json.Unmarshal(calls[0].Body, &m); err != nil {
		f.t.Fatalf("%s %s: body %q: %v", method, path, calls[0].Body, err)
	}
	return m
}

// fixtureDir is found before any test changes the working folder.
var fixtureDir, _ = filepath.Abs(filepath.Join("testdata", "fixtures"))

// fixture is a file from testdata/fixtures: orbits-go's spec-checked
// fixtures, and a few more written to the same schemas.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// result is what one run of the CLI printed.
type result struct {
	stdout, stderr string
	err            error
}

// opts tune one run.
type opts struct {
	stdin       string
	interactive bool
	// noToken leaves ORBITS_TOKEN unset (a stored sign-in, or none).
	noToken bool
	// cfg is the config the run starts with (default: empty).
	cfg *config.Config
	// configDir holds config.json and credentials.json (default: a new one).
	configDir string
}

// The team and project of the folder tests run in, when linked.
const (
	teamID    = "team_test1"
	projectID = "prj_test1"
	projPath  = "/teams/" + teamID + "/projects/" + projectID
)

// linkHere links a fresh working folder to prj_test1 in team_test1 and
// moves into it.
func linkHere(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := link.Write(dir, link.Link{TeamID: teamID, TeamSlug: "acme", ProjectID: projectID, ProjectSlug: "web"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// unlinkedHere moves into a fresh folder that is not linked.
func unlinkedHere(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// run runs the CLI against srv with a token in the environment, no
// keychain, no retries and fast polling, without a terminal.
func run(t *testing.T, srv *httptest.Server, stdin string, args ...string) result {
	t.Helper()
	return runWith(t, srv, opts{stdin: stdin}, args...)
}

func runWith(t *testing.T, srv *httptest.Server, o opts, args ...string) result {
	t.Helper()
	if o.noToken {
		t.Setenv(EnvToken, "")
	} else {
		t.Setenv(EnvToken, testToken)
	}
	if srv != nil {
		t.Setenv(EnvAPIURL, srv.URL+"/v1")
	}
	if o.configDir == "" {
		o.configDir = t.TempDir()
	}
	t.Setenv("ORBITS_CONFIG_DIR", o.configDir)
	t.Setenv("ORBITS_NO_KEYRING", "1")
	t.Setenv(EnvTeam, "")
	t.Setenv(EnvProfile, "")
	t.Setenv("ORBITS_NO_UPDATE_NOTIFIER", "1")
	oldPoll, oldRuntime := pollInterval, runtimePoll
	pollInterval, runtimePoll = time.Millisecond, time.Millisecond
	t.Cleanup(func() { pollInterval, runtimePoll = oldPoll, oldRuntime })

	// Without a config given, the run reads ORBITS_CONFIG_DIR's, as for real.
	cfg := o.cfg
	zero := 0
	var out, errOut bytes.Buffer
	a := &app{
		cfg:         cfg,
		out:         &output.Printer{Out: &out, Err: &errOut},
		stdin:       strings.NewReader(o.stdin),
		interactive: func() bool { return o.interactive },
		maxRetries:  &zero,
	}
	root := newRoot(a)
	root.SetArgs(args)
	root.SetOut(&errOut)
	root.SetErr(&errOut)
	err := root.Execute()
	return result{out.String(), errOut.String(), err}
}

func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("%s lacks %q:\n%s", what, w, got)
		}
	}
}
