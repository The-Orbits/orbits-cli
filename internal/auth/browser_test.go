package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const grantBody = `{"token":"orb_live_abcdefghijklmnopqrstuvwxyz012345","token_id":"tok_1","token_name":"CLI on my mac","team":{"id":"team_1","slug":"acme","name":"Acme"},"access":"full","expires_at":"2027-02-08T10:00:00Z","api_url":"https://stagingapi.theorbits.app"}`

// webApp stands in for the Orbits web app: /cli/authorize answers 200 (the
// probe), and /api/cli/token hands out the grant only for the code it issued
// and the verifier whose S256 is the challenge the browser brought.
type webApp struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	challenge string
	exchanges []map[string]string
	probes    int
	// tokenStatus, when set, is how /api/cli/token answers instead.
	tokenStatus int
	retryAfter  string
}

func newWebApp(t *testing.T) *webApp {
	t.Helper()
	w := &webApp{t: t}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/authorize":
			w.mu.Lock()
			w.probes++
			w.mu.Unlock()
			rw.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(rw, "<html>Start from your terminal</html>")
		case "/api/cli/token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.mu.Lock()
			w.exchanges = append(w.exchanges, body)
			challenge, status, retry := w.challenge, w.tokenStatus, w.retryAfter
			w.mu.Unlock()
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("exchange: %s with Content-Type %q", r.Method, r.Header.Get("Content-Type"))
			}
			sum := sha256.Sum256([]byte(body["code_verifier"]))
			switch {
			case status == http.StatusTooManyRequests:
				rw.Header().Set("Retry-After", retry)
				rw.Header().Set("Content-Type", "application/problem+json")
				rw.WriteHeader(status)
				_, _ = io.WriteString(rw, `{"status":429,"code":"RATE_LIMITED","detail":"Too many attempts. Wait a minute and try again."}`)
			case status != 0, body["code"] != "the-code", base64.RawURLEncoding.EncodeToString(sum[:]) != challenge:
				rw.Header().Set("Content-Type", "application/problem+json")
				rw.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(rw, `{"status":400,"code":"INVALID_GRANT","detail":"This sign-in code is invalid or has expired. Run orbits login again."}`)
			default:
				_, _ = io.WriteString(rw, grantBody)
			}
		default:
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(w.srv.Close)
	return w
}

var base64url = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// browse plays the browser: it checks the authorize URL as the web app's
// contract does, keeps the challenge, and calls the CLI's callback with the
// query each step gives (one per callback), sending each page it got back.
func (w *webApp) browse(authorize string, steps []func(state string) url.Values, pages chan<- string) error {
	u, err := url.Parse(authorize)
	if err != nil {
		return err
	}
	q := u.Query()
	if u.Path != "/cli/authorize" || len(q) != 4 {
		w.t.Errorf("authorize URL %s", authorize)
	}
	port, err := strconv.Atoi(q.Get("port"))
	if err != nil || port < 1024 || port > 65535 {
		w.t.Errorf("port %q", q.Get("port"))
	}
	if s := q.Get("state"); len(s) != 32 || !base64url.MatchString(s) {
		w.t.Errorf("state %q: want 32 base64url characters", s)
	}
	if c := q.Get("challenge"); len(c) != 43 || !base64url.MatchString(c) {
		w.t.Errorf("challenge %q: want 43 base64url characters", c)
	}
	if h := q.Get("host"); len(h) > 60 || !regexp.MustCompile(`^[A-Za-z0-9 .@_-]*$`).MatchString(h) {
		w.t.Errorf("host %q", h)
	}
	w.mu.Lock()
	w.challenge = q.Get("challenge")
	w.mu.Unlock()
	go func() {
		for _, step := range steps {
			res, err := http.Get("http://127.0.0.1:" + q.Get("port") + "/callback?" + step(q.Get("state")).Encode())
			if err != nil {
				pages <- err.Error()
				continue
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			pages <- strconv.Itoa(res.StatusCode) + " " + string(b)
		}
	}()
	return nil
}

func approveWith(code string) func(string) url.Values {
	return func(state string) url.Values { return url.Values{"state": {state}, "code": {code}} }
}

func TestLoginApproved(t *testing.T) {
	web := newWebApp(t)
	pages := make(chan string, 1)
	b := &Browser{
		WebURL: web.srv.URL,
		Host:   "my mac! (home)",
		Open: func(authorize string) error {
			if !strings.Contains(authorize, "host=my+mac+home") {
				t.Errorf("host not passed, or not sanitized: %s", authorize)
			}
			return web.browse(authorize, []func(string) url.Values{approveWith("the-code")}, pages)
		},
	}
	grant, err := b.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if grant.Token != "orb_live_abcdefghijklmnopqrstuvwxyz012345" || grant.Team.Slug != "acme" || grant.Access != "full" || grant.TokenID != "tok_1" {
		t.Fatalf("grant = %+v", grant)
	}
	if grant.APIBase() != "https://stagingapi.theorbits.app/v1" {
		t.Fatalf("APIBase = %q", grant.APIBase())
	}
	if p := <-pages; !strings.HasPrefix(p, "200") || !strings.Contains(p, "signed in") {
		t.Fatalf("callback page: %s", p)
	}
	if len(web.exchanges) != 1 {
		t.Fatalf("%d exchanges", len(web.exchanges))
	}
	if v := web.exchanges[0]["code_verifier"]; len(v) != 43 || !base64url.MatchString(v) {
		t.Fatalf("verifier %q: want 43 base64url characters", v)
	}
	if web.probes != 1 {
		t.Fatalf("probes = %d", web.probes)
	}
}

// A callback with another state is someone else's (or a stale tab): it is
// refused with 400 and the CLI goes on waiting for its own.
func TestLoginStateMismatchIsRefused(t *testing.T) {
	web := newWebApp(t)
	pages := make(chan string, 2)
	b := &Browser{WebURL: web.srv.URL, Open: func(authorize string) error {
		return web.browse(authorize, []func(string) url.Values{
			func(string) url.Values {
				return url.Values{"state": {"forged-state-forged-state-forged!"}, "code": {"the-code"}}
			},
			approveWith("the-code"),
		}, pages)
	}}
	grant, err := b.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if grant.Token == "" {
		t.Fatal("no token")
	}
	if p := <-pages; !strings.HasPrefix(p, "400") || !strings.Contains(p, "does not match") {
		t.Fatalf("forged callback answered: %s", p)
	}
	if p := <-pages; !strings.HasPrefix(p, "200") {
		t.Fatalf("real callback answered: %s", p)
	}
	if len(web.exchanges) != 1 {
		t.Fatalf("the forged code was traded: %d exchanges", len(web.exchanges))
	}
}

func TestLoginCancelled(t *testing.T) {
	web := newWebApp(t)
	pages := make(chan string, 1)
	b := &Browser{WebURL: web.srv.URL, Open: func(authorize string) error {
		return web.browse(authorize, []func(string) url.Values{func(state string) url.Values {
			return url.Values{"state": {state}, "error": {"access_denied"}}
		}}, pages)
	}}
	if _, err := b.Login(context.Background()); !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
	if p := <-pages; !strings.Contains(p, "Sign-in cancelled") {
		t.Fatalf("callback page: %s", p)
	}
	if len(web.exchanges) != 0 {
		t.Fatal("a cancelled sign-in traded a code")
	}
}

// A code the web app refuses (wrong verifier, expired, used) is one 400
// INVALID_GRANT; the tab and the terminal both say the sign-in failed.
func TestLoginExchangeRefused(t *testing.T) {
	web := newWebApp(t)
	pages := make(chan string, 1)
	b := &Browser{WebURL: web.srv.URL, Open: func(authorize string) error {
		return web.browse(authorize, []func(string) url.Values{approveWith("an-expired-code")}, pages)
	}}
	_, err := b.Login(context.Background())
	if !errors.Is(err, ErrInvalidGrant) || !strings.Contains(err.Error(), "orbits login") {
		t.Fatalf("err = %v, want ErrInvalidGrant", err)
	}
	if p := <-pages; !strings.Contains(p, "Sign-in failed") || strings.Contains(p, "signed in") {
		t.Fatalf("the tab must say the sign-in failed: %s", p)
	}
}

func TestExchangeRateLimited(t *testing.T) {
	web := newWebApp(t)
	web.tokenStatus, web.retryAfter = http.StatusTooManyRequests, "42"
	b := &Browser{WebURL: web.srv.URL}
	_, err := b.Exchange(context.Background(), "the-code", "v")
	var limited *RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter != 42*time.Second || !strings.Contains(err.Error(), "42s") {
		t.Fatalf("err = %v, want RateLimitedError after 42s", err)
	}
}

func TestExchangeAnswerWithoutAToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"token":"","team":{}}`)
	}))
	t.Cleanup(srv.Close)
	if _, err := (&Browser{WebURL: srv.URL}).Exchange(context.Background(), "c", "v"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginVerifyFailureReachesTheBrowser(t *testing.T) {
	web := newWebApp(t)
	pages := make(chan string, 1)
	b := &Browser{
		WebURL: web.srv.URL,
		Open: func(authorize string) error {
			return web.browse(authorize, []func(string) url.Values{approveWith("the-code")}, pages)
		},
		Verify: func(context.Context, *Grant) error { return errors.New("the token did not work") },
	}
	if _, err := b.Login(context.Background()); err == nil {
		t.Fatal("a failed check must fail the sign-in")
	}
	if p := <-pages; !strings.Contains(p, "Sign-in failed") || strings.Contains(p, "signed in") {
		t.Fatalf("the tab must say the sign-in failed: %s", p)
	}
}

func TestLoginTimesOut(t *testing.T) {
	web := newWebApp(t)
	b := &Browser{WebURL: web.srv.URL, Timeout: 50 * time.Millisecond}
	if _, err := b.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "no approval") || !strings.Contains(err.Error(), "--with-token") {
		t.Fatalf("err = %v", err)
	}
}

// A web app without the CLI pages answers 404: the CLI says to use a token,
// at once, without opening a browser.
func TestLoginNotOffered(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	b := &Browser{WebURL: srv.URL, Timeout: time.Second, Open: func(string) error {
		t.Error("the browser must not open when the web app has no sign-in page")
		return nil
	}}
	_, err := b.Login(context.Background())
	if !errors.Is(err, ErrNotOffered) || !strings.Contains(err.Error(), "--with-token") {
		t.Fatalf("err = %v, want ErrNotOffered naming --with-token", err)
	}
}

func TestPromptGetsTheURL(t *testing.T) {
	web := newWebApp(t)
	var prompted string
	var wasOpened bool
	b := &Browser{WebURL: web.srv.URL + "/", Timeout: 50 * time.Millisecond,
		Open:   func(string) error { return errors.New("no browser here") },
		Prompt: func(u string, opened bool) { prompted, wasOpened = u, opened },
	}
	_, _ = b.Login(context.Background())
	if !strings.HasPrefix(prompted, web.srv.URL+"/cli/authorize?") || wasOpened {
		t.Fatalf("Prompt(%q, %v)", prompted, wasOpened)
	}
}

func TestNewRequestIsPKCE(t *testing.T) {
	r := NewRequest()
	sum := sha256.Sum256([]byte(r.Verifier))
	if r.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || len(r.Verifier) != 43 || len(r.State) != 32 || len(r.Challenge) != 43 {
		t.Fatalf("request %+v", r)
	}
	if NewRequest().State == r.State {
		t.Fatal("states repeat")
	}
}

func TestSanitizeHost(t *testing.T) {
	for in, want := range map[string]string{
		"johns-MacBook-Pro":     "johns-MacBook-Pro",
		" dev box ":             "dev box",
		"ci@runner_1.local":     "ci@runner_1.local",
		"a/b\\c<d>\"e'":         "abcde",
		"ünïcode":               "ncode",
		strings.Repeat("x", 80): strings.Repeat("x", 60),
		"\x1b]0;evil\x07":       "0evil",
	} {
		if got := SanitizeHost(in); got != want {
			t.Errorf("SanitizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGrantAPIBase(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.theorbits.app":         "https://api.theorbits.app/v1",
		"https://api.theorbits.app/":        "https://api.theorbits.app/v1",
		"https://api.theorbits.app/v1":      "https://api.theorbits.app/v1",
		"http://127.0.0.1:8080":             "http://127.0.0.1:8080/v1",
		"http://api.theorbits.app":          "",
		"https://user:pw@api.theorbits.app": "",
		"":                                  "",
		"not a url":                         "",
	} {
		if got := (&Grant{APIURL: in}).APIBase(); got != want {
			t.Errorf("APIBase(%q) = %q, want %q", in, got, want)
		}
	}
}
