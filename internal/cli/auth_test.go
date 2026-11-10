package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/The-Orbits/orbits-cli/internal/config"
	"github.com/The-Orbits/orbits-cli/internal/link"
)

// webOrigin stands in for https://orbits.africa: the authorize page (the
// probe's 200) and the token exchange, which checks PKCE. answer, when set,
// replaces a good grant's body.
type webOrigin struct {
	srv *httptest.Server

	mu        sync.Mutex
	challenge string
	status    int
	answer    string
}

func newWebOrigin(t *testing.T, apiURL string) *webOrigin {
	t.Helper()
	w := &webOrigin{}
	grant := `{"token":"` + testToken + `","token_id":"tok_test1","token_name":"CLI on dev box","team":{"id":"team_test1","slug":"acme","name":"Acme"},"access":"full","expires_at":"2027-02-08T10:00:00Z","api_url":"` + apiURL + `"}`
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/authorize":
			rw.WriteHeader(http.StatusOK)
		case "/api/cli/token":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			sum := sha256.Sum256([]byte(b["code_verifier"]))
			w.mu.Lock()
			ok := b["code"] == "the-code" && base64.RawURLEncoding.EncodeToString(sum[:]) == w.challenge && w.status == 0
			w.mu.Unlock()
			if !ok {
				rw.Header().Set("Content-Type", "application/problem+json")
				rw.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(rw, `{"status":400,"code":"INVALID_GRANT","detail":"This sign-in code is invalid or has expired. Run orbits login again."}`)
				return
			}
			_, _ = io.WriteString(rw, grant)
		default:
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(w.srv.Close)
	return w
}

// browser plays the person: it opens the authorize URL and answers the
// CLI's callback with query(state).
func (w *webOrigin) browser(t *testing.T, query func(state string) url.Values) {
	t.Helper()
	old := openBrowser
	t.Cleanup(func() { openBrowser = old })
	openBrowser = func(target string) error {
		u, err := url.Parse(target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(target, w.srv.URL+"/cli/authorize?") {
			t.Errorf("opened %s", target)
		}
		q := u.Query()
		w.mu.Lock()
		w.challenge = q.Get("challenge")
		w.mu.Unlock()
		go func() {
			res, err := http.Get("http://127.0.0.1:" + q.Get("port") + "/callback?" + query(q.Get("state")).Encode())
			if err == nil {
				res.Body.Close()
			}
		}()
		return nil
	}
}

func approve(state string) url.Values { return url.Values{"state": {state}, "code": {"the-code"}} }

func readConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

// The whole browser sign-in, in process: the web app approves, the code is
// traded with PKCE, the token is checked on the API the web app named, and
// the profile keeps the team, the access and that API.
func TestLoginInTheBrowser(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json"))
	web := newWebOrigin(t, api.URL)
	web.browser(t, approve)
	t.Setenv(EnvWebURL, web.srv.URL)
	t.Setenv(EnvAPIURL, "")
	dir := t.TempDir()

	r := runWith(t, nil, opts{noToken: true, configDir: dir}, "login")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.stderr)
	}
	mustContain(t, "stderr", r.stderr, "Opened your browser", "Signed in to Acme as person@example.com, full access", `profile "default"`, "credentials.json")
	if len(f.sent("GET", "/me")) != 1 {
		t.Fatal("the token was not checked on the API the web app named")
	}
	cfg := readConfig(t, dir)
	p := cfg.Profiles["default"]
	want := config.Profile{TeamID: "team_test1", TeamSlug: "acme", TeamName: "Acme", TokenID: "tok_test1", TokenName: "CLI on dev box", Access: "full", ExpiresAt: "2027-02-08T10:00:00Z", APIURL: api.URL + "/v1", UserEmail: "person@example.com"}
	if p != want || cfg.Current != "default" {
		t.Fatalf("profile = %+v\nwant      %+v", p, want)
	}
	creds, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if !strings.Contains(string(creds), testToken) {
		t.Fatal("the token was not stored")
	}
	if strings.Contains(r.stdout+r.stderr, testToken) {
		t.Fatal("the token was printed")
	}

	// The stored sign-in works on its own: whoami uses the profile's API.
	r = runWith(t, nil, opts{noToken: true, configDir: dir}, "whoami")
	if r.err != nil {
		t.Fatal(r.err)
	}
	mustContain(t, "whoami", r.stdout, "Test User", "person@example.com", "Acme", "CLI on dev box", "full", api.URL+"/v1", "profile default")

	// And commands act in the profile's team.
	f.on("GET /teams/acme/projects", 200, fixture(t, "listProjects.page2.json"))
	r = runWith(t, nil, opts{noToken: true, configDir: dir}, "projects", "ls", "-q")
	if r.err != nil || r.stdout != "three\n" {
		t.Fatalf("stdout %q, err %v", r.stdout, r.err)
	}
}

func TestLoginCancelledInTheBrowser(t *testing.T) {
	unlinkedHere(t)
	_, api := newFakeAPI(t)
	web := newWebOrigin(t, api.URL)
	web.browser(t, func(state string) url.Values { return url.Values{"state": {state}, "error": {"access_denied"}} })
	t.Setenv(EnvWebURL, web.srv.URL)
	dir := t.TempDir()
	r := runWith(t, api, opts{noToken: true, configDir: dir}, "login")
	if r.err == nil || !strings.Contains(r.err.Error(), "cancelled in the browser") {
		t.Fatalf("err = %v", r.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		t.Fatal("a cancelled sign-in saved a profile")
	}
}

func TestLoginExchangeFails(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	web := newWebOrigin(t, api.URL)
	web.status = http.StatusBadRequest
	web.browser(t, approve)
	t.Setenv(EnvWebURL, web.srv.URL)
	r := runWith(t, api, opts{noToken: true}, "login")
	if r.err == nil || !strings.Contains(r.err.Error(), "invalid or has expired") {
		t.Fatalf("err = %v", r.err)
	}
	if len(f.calls) != 0 {
		t.Fatal("called the API without a token")
	}
}

// A token the API refuses is not kept, and the tab is told; the person is
// told which token to revoke.
func TestLoginTokenRefusedByTheAPI(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 401, `{"status":401,"title":"Unauthorized","detail":"Invalid token"}`)
	web := newWebOrigin(t, api.URL)
	web.browser(t, approve)
	t.Setenv(EnvWebURL, web.srv.URL)
	dir := t.TempDir()
	r := runWith(t, api, opts{noToken: true, configDir: dir}, "login")
	if r.err == nil || !strings.Contains(r.err.Error(), "tok_test1") || !strings.Contains(r.err.Error(), "refused the token") {
		t.Fatalf("err = %v", r.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.json")); err == nil {
		t.Fatal("a refused token was stored")
	}
}

func TestLoginNotOfferedSaysUseAToken(t *testing.T) {
	unlinkedHere(t)
	web := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(web.Close)
	t.Setenv(EnvWebURL, web.URL)
	r := runWith(t, nil, opts{noToken: true}, "login")
	if r.err == nil || !strings.Contains(r.err.Error(), "--with-token") {
		t.Fatalf("err = %v", r.err)
	}
}

func TestLoginWithToken(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json"))
	dir := t.TempDir()
	r := runWith(t, api, opts{noToken: true, configDir: dir, stdin: testToken + "\n"}, "--profile", "ci", "login", "--with-token")
	if r.err != nil {
		t.Fatal(r.err)
	}
	cfg := readConfig(t, dir)
	p := cfg.Profiles["ci"]
	if p.TeamSlug != "acme" || p.TeamID != "team_test1" || p.Access != "" || p.APIURL != api.URL+"/v1" {
		t.Fatalf("profile = %+v", p)
	}
	if cfg.Current != "ci" {
		t.Fatalf("current = %q: the first sign-in becomes current", cfg.Current)
	}
}

func TestLoginWithTokenRefusesWhatIsNotAToken(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	r := runWith(t, api, opts{noToken: true, stdin: "PAN_something\n"}, "login", "--with-token")
	if r.err == nil || !strings.Contains(r.err.Error(), "orb_live_") || len(f.calls) != 0 {
		t.Fatalf("err = %v, calls %d", r.err, len(f.calls))
	}
}

func TestLogout(t *testing.T) {
	unlinkedHere(t)
	dir := t.TempDir()
	t.Setenv("ORBITS_CONFIG_DIR", dir)
	t.Setenv("ORBITS_NO_KEYRING", "1")
	if _, err := config.SaveToken("default", testToken); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Current: "default", Profiles: map[string]config.Profile{"default": {TeamSlug: "acme", TokenName: "CLI on dev box"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	r := runWith(t, nil, opts{noToken: true, configDir: dir}, "logout")
	if r.err != nil {
		t.Fatal(r.err)
	}
	mustContain(t, "stderr", r.stderr, `Signed out of profile "default"`, "CLI on dev box", "Team › Settings › API tokens")
	if tok, _ := config.LoadToken("default"); tok != "" {
		t.Fatal("the token is still stored")
	}
	if got := readConfig(t, dir); len(got.Profiles) != 0 || got.Current != "" {
		t.Fatalf("config = %+v", got)
	}
	r = runWith(t, nil, opts{noToken: true, configDir: dir}, "whoami")
	if !IsNotSignedIn(r.err) {
		t.Fatalf("err = %v", r.err)
	}
}

func TestWhoamiWithTheEnvironmentToken(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json"))
	r := run(t, api, "", "whoami", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var w map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &w); err != nil {
		t.Fatal(err)
	}
	if w["source"] != EnvToken || w["team"].(map[string]any)["slug"] != "acme" || w["token"] != nil {
		t.Fatalf("whoami = %v", w)
	}
}

// /me says what the token in effect may do, so whoami shows it whatever the token came from.
func TestWhoamiShowsTheTokensAccessAndProject(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.token.json"))
	r := run(t, api, "", "whoami", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var w struct {
		Token *tokenInfo `json:"token"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &w); err != nil {
		t.Fatal(err)
	}
	if w.Token == nil || *w.Token != (tokenInfo{ID: "tok_test1", Access: "read", ProjectID: "prj_test1", Project: "web"}) {
		t.Fatalf("token = %+v", w.Token)
	}
	r = run(t, api, "", "whoami")
	if r.err != nil {
		t.Fatal(r.err)
	}
	mustContain(t, "whoami", r.stdout, "tok_test1", "read only", "only web", "prj_test1")
	if strings.Contains(r.stdout, "Expires") {
		t.Fatalf("an ORBITS_TOKEN's expiry is unknown, not shown:\n%s", r.stdout)
	}
}

func TestLoginWithTokenRecordsWhatTheTokenMayDo(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.token.json"))
	dir := t.TempDir()
	r := runWith(t, api, opts{noToken: true, configDir: dir, stdin: testToken + "\n"}, "login", "--with-token")
	if r.err != nil {
		t.Fatal(r.err)
	}
	p := readConfig(t, dir).Profiles["default"]
	if p.Access != "read" || p.TokenID != "tok_test1" || p.TeamID != "team_test1" {
		t.Fatalf("profile = %+v", p)
	}
	mustContain(t, "stderr", r.stderr, "read only")
}

func TestTeams(t *testing.T) {
	unlinkedHere(t)
	dir := t.TempDir()
	t.Setenv("ORBITS_CONFIG_DIR", dir)
	t.Setenv("ORBITS_NO_KEYRING", "1")
	for _, n := range []string{"work", "side"} {
		if _, err := config.SaveToken(n, testToken); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Current: "work", Profiles: map[string]config.Profile{
		"work": {TeamID: "team_test1", TeamSlug: "acme", TeamName: "Acme", Access: "full"},
		"side": {TeamID: "team_test2", TeamSlug: "side-co", TeamName: "Side Co", Access: "read"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	f, api := newFakeAPI(t)
	f.on("GET /teams", 200, fixture(t, "listTeams.json"))

	r := runWith(t, api, opts{noToken: true, configDir: dir}, "teams", "list")
	if r.err != nil {
		t.Fatal(r.err)
	}
	mustContain(t, "teams list", r.stdout, "Acme", "Side Co", "owner", "read")
	if !strings.Contains(r.stdout, "*   Acme") {
		t.Fatalf("the current team is not marked:\n%s", r.stdout)
	}

	r = runWith(t, api, opts{noToken: true, configDir: dir}, "teams", "switch", "side-co")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := readConfig(t, dir); got.Current != "side" {
		t.Fatalf("current = %q", got.Current)
	}
	r = runWith(t, api, opts{noToken: true, configDir: dir}, "teams", "switch", "elsewhere")
	if r.err == nil || !strings.Contains(r.err.Error(), "orbits --profile elsewhere login") {
		t.Fatalf("err = %v", r.err)
	}
}

func TestLinkByName(t *testing.T) {
	dir := unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json")).
		on("GET /teams/acme/projects/web", 200, fixture(t, "getProject.upload.json")).
		on("GET /teams", 200, fixture(t, "listTeams.json"))
	r := run(t, api, "", "link", "web")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.stderr)
	}
	l, err := link.Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Link != (link.Link{TeamID: "team_test1", TeamSlug: "acme", ProjectID: "prj_test1", ProjectSlug: "web"}) {
		t.Fatalf("link = %+v", l.Link)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(gi) != ".orbits\n" {
		t.Fatalf(".gitignore = %q", gi)
	}
	mustContain(t, "stderr", r.stderr, "Linked", "acme/web", "Added .orbits to .gitignore", "orbits deploy")

	// From now on, commands here use it.
	f.on("GET "+projPath, 200, fixture(t, "getProject.upload.json"))
	if r := run(t, api, "", "projects", "get", "-q"); r.err != nil || r.stdout != "prj_test1\n" {
		t.Fatalf("stdout %q err %v", r.stdout, r.err)
	}
	if r := run(t, api, "", "unlink"); r.err != nil {
		t.Fatal(r.err)
	}
	if _, err := link.Find(dir); err != link.ErrNotLinked {
		t.Fatalf("still linked: %v", err)
	}
}

func TestLinkPicksAtATerminal(t *testing.T) {
	dir := unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json")).
		on("GET /teams/acme/projects", 200, fixture(t, "listProjects.page1.json")).
		on("GET /teams/acme/projects", 200, fixture(t, "listProjects.page2.json")).
		on("GET /teams", 200, fixture(t, "listTeams.json"))
	r := runWith(t, api, opts{interactive: true, stdin: "2\n"}, "link")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.stderr)
	}
	l, err := link.Find(dir)
	if err != nil || l.ProjectID != "prj_two" {
		t.Fatalf("link = %+v, %v", l, err)
	}
}

func TestLinkWithoutATerminalNeedsAName(t *testing.T) {
	unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json"))
	r := run(t, api, "", "link")
	if !IsUsage(r.err) || !strings.Contains(r.err.Error(), "orbits link <project>") {
		t.Fatalf("err = %v", r.err)
	}
}

func TestLinkCreate(t *testing.T) {
	dir := unlinkedHere(t)
	f, api := newFakeAPI(t)
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json")).
		on("POST /teams/acme/projects", 201, fixture(t, "getProject.upload.json")).
		on("GET /teams", 200, fixture(t, "listTeams.json"))
	r := run(t, api, "", "link", "--create")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := f.sentJSON("POST", "/teams/acme/projects"); got["name"] != filepath.Base(dir) || got["source_kind"] != "upload" {
		t.Fatalf("create body = %v", got)
	}
}

func TestNotSignedIn(t *testing.T) {
	unlinkedHere(t)
	r := runWith(t, nil, opts{noToken: true}, "projects", "list")
	if !IsNotSignedIn(r.err) {
		t.Fatalf("err = %v", r.err)
	}
}

func TestOpenPrintsWithoutATerminal(t *testing.T) {
	linkHere(t)
	f, api := newFakeAPI(t)
	f.on("GET "+projPath, 200, fixture(t, "getProject.upload.json"))
	r := run(t, api, "", "open")
	if r.err != nil || r.stdout != "https://orbits.africa/acme/web\n" {
		t.Fatalf("stdout %q err %v", r.stdout, r.err)
	}
	r = run(t, api, "", "open", "--site")
	if r.err != nil || r.stdout != "https://web.theorbits.app\n" {
		t.Fatalf("stdout %q err %v", r.stdout, r.err)
	}
	t.Setenv(EnvWebURL, "https://staging.orbits.example")
	if r := run(t, api, "", "open"); r.stdout != "https://staging.orbits.example/acme/web\n" {
		t.Fatalf("ORBITS_WEB_URL not used: %q", r.stdout)
	}
}

func TestVersion(t *testing.T) {
	r := runWith(t, nil, opts{noToken: true}, "version")
	if r.err != nil || r.stdout != "orbits dev\n" {
		t.Fatalf("stdout %q err %v", r.stdout, r.err)
	}
}

func TestDefaultEndpoints(t *testing.T) {
	t.Setenv(EnvAPIURL, "")
	t.Setenv(EnvWebURL, "")
	a := &app{cfg: &config.Config{Profiles: map[string]config.Profile{}}}
	if a.baseURL() != "https://api.theorbits.app/v1" || a.webURL() != "https://orbits.africa" {
		t.Fatalf("baseURL %q webURL %q", a.baseURL(), a.webURL())
	}
	t.Setenv(EnvAPIURL, "https://stagingapi.theorbits.app/v1/")
	if a.baseURL() != "https://stagingapi.theorbits.app/v1" {
		t.Fatalf("ORBITS_API_URL: %q", a.baseURL())
	}
}
