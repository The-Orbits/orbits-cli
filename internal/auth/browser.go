// Package auth signs the CLI in through the Orbits web app, the way
// `gh auth login` does: a browser approval, a loopback callback with a
// one-time code, and a PKCE-guarded exchange of that code for an API token;
// see docs/cli-auth-protocol.md.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultWebURL is the web app that approves the CLI; ORBITS_WEB_URL
// replaces it (staging).
const DefaultWebURL = "https://orbits.africa"

// GrantTeam is the team the approved token acts in.
type GrantTeam struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Grant is the web app's answer to a good code: the token, once, and what it is for.
type Grant struct {
	Token     string    `json:"token"`
	TokenID   string    `json:"token_id"`
	TokenName string    `json:"token_name"`
	Team      GrantTeam `json:"team"`
	// Access is "full" or "read".
	Access string `json:"access"`
	// ExpiresAt is RFC 3339, or nil for a token that never expires.
	ExpiresAt *string `json:"expires_at"`
	// APIURL is the API the web app talks to, without /v1.
	APIURL string `json:"api_url"`
}

// ErrDenied is the person choosing Cancel in the browser.
var ErrDenied = errors.New("the sign-in was cancelled in the browser")

// ErrNotOffered is a web app without the CLI sign-in pages.
var ErrNotOffered = errors.New("this web app does not offer browser sign-in for the CLI\n" +
	"Make an API token under Team › Settings › API tokens and run:\n" +
	"  orbits login --with-token < token.txt")

// ErrInvalidGrant is the web app refusing the code: expired (it lasts two
// minutes), used already, or not for this verifier.
var ErrInvalidGrant = errors.New("the web app refused the sign-in code: it is invalid or has expired\nRun orbits login again")

// RateLimitedError is the web app asking the CLI to slow down.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	wait := "a minute"
	if e.RetryAfter > 0 {
		wait = e.RetryAfter.String()
	}
	return "too many sign-in attempts from this network: wait " + wait + " and run orbits login again"
}

// Browser runs one browser sign-in.
type Browser struct {
	WebURL string // https://orbits.africa
	Host   string // this machine's name, for the token's name
	HTTP   *http.Client
	// Open opens a URL in the browser; nil means only Prompt is called.
	Open   func(string) error
	Prompt func(authorizeURL string, opened bool)
	// Timeout for the whole approval. Default 5 minutes.
	Timeout time.Duration
	// Verify checks the grant (that the token works) before the browser tab
	// is told the sign-in worked. Optional.
	Verify func(context.Context, *Grant) error
}

func (b *Browser) base() string { return strings.TrimRight(b.WebURL, "/") }

func (b *Browser) client() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// probe asks the web app whether it has the authorize page (it answers 200
// with no request), so one without it fails at once instead of after the
// browser shows "page not found" and the wait times out. Only a 404 counts:
// any other answer, or none, is left to the sign-in itself.
func (b *Browser) probe(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, b.base()+"/cli/authorize", nil)
	if err != nil {
		return nil
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		// A redirect (to the sign-in page) means the route exists.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if b.HTTP != nil {
		client.Transport = b.HTTP.Transport
	}
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	_ = res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotOffered
	}
	return nil
}

// Request is one sign-in's secrets and the URL the browser opens.
type Request struct {
	Verifier  string
	State     string
	Challenge string
}

// NewRequest makes a PKCE verifier (43 base64url characters from 32 random
// bytes), its S256 challenge, and a state (32 characters from 24 bytes).
func NewRequest() Request {
	verifier := randomString(32)
	sum := sha256.Sum256([]byte(verifier))
	return Request{Verifier: verifier, State: randomString(24), Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}
}

// AuthorizeURL is the page the browser opens for this request.
func (b *Browser) AuthorizeURL(r Request, port int) string {
	return b.base() + "/cli/authorize?" + url.Values{
		"port":      {strconv.Itoa(port)},
		"state":     {r.State},
		"challenge": {r.Challenge},
		"host":      {SanitizeHost(b.Host)},
	}.Encode()
}

// Login runs the sign-in: listen on 127.0.0.1, open the authorize page, wait
// for the callback, trade its code for the token, and Verify it.
func (b *Browser) Login(ctx context.Context) (*Grant, error) {
	if err := b.probe(ctx); err != nil {
		return nil, err
	}
	r := NewRequest()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not listen for the browser's answer: %w", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	// The sign-in's outcome, for the browser tab still waiting on its callback.
	outcome := make(chan error, 1)
	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/callback" {
				http.NotFound(w, req)
				return
			}
			q := req.URL.Query()
			// Anything without our state is not the web app answering us.
			if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(r.State)) != 1 {
				http.Error(w, "This sign-in link does not match the one the CLI started. Run orbits login again.", http.StatusBadRequest)
				return
			}
			switch {
			case q.Get("error") != "":
				page(w, "Sign-in cancelled", "Nothing was created. You can close this tab.")
				select {
				case results <- result{err: ErrDenied}:
				default:
				}
			case q.Get("code") != "":
				select {
				case results <- result{code: q.Get("code")}:
				default:
					page(w, "Already signed in", "This sign-in has already been used. You can close this tab.")
					return
				}
				// Answer only once the token is collected and checked, so the
				// tab never says "signed in" when the terminal says otherwise.
				select {
				case err := <-outcome:
					if err != nil {
						page(w, "Sign-in failed", firstLine(err.Error())+". Your terminal has the details.")
					} else {
						page(w, "You're signed in", "The Orbits CLI has its token. You can close this tab and go back to your terminal.")
					}
				case <-time.After(45 * time.Second):
					page(w, "Still working", "The CLI is still collecting its token. Your terminal will say when it is done.")
				}
			default:
				http.Error(w, "Missing code.", http.StatusBadRequest)
			}
		}),
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	authorize := b.AuthorizeURL(r, port)
	opened := false
	if b.Open != nil {
		opened = b.Open(authorize) == nil
	}
	if b.Prompt != nil {
		b.Prompt(authorize, opened)
	}

	timeout := b.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var code string
	select {
	case <-wait.Done():
		if errors.Is(wait.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("no approval within %s: run orbits login again\nWithout a browser, sign in with a token instead: orbits login --with-token < token.txt", timeout)
		}
		return nil, wait.Err()
	case res := <-results:
		if res.err != nil {
			return nil, res.err
		}
		code = res.code
	}
	grant, err := b.Exchange(ctx, code, r.Verifier)
	if err == nil && b.Verify != nil {
		err = b.Verify(ctx, grant)
	}
	outcome <- err
	if err != nil {
		return nil, err
	}
	return grant, nil
}

// Exchange trades a code and its verifier for the token: POST /api/cli/token.
func (b *Browser) Exchange(ctx context.Context, code, verifier string) (*Grant, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": verifier})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base()+"/api/cli/token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := b.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s to collect the token: %w", b.base(), err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotOffered
	case http.StatusBadRequest:
		return nil, ErrInvalidGrant
	case http.StatusTooManyRequests:
		secs, _ := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After")))
		return nil, &RateLimitedError{RetryAfter: time.Duration(secs) * time.Second}
	default:
		var problem struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data, &problem) == nil && problem.Detail != "" {
			return nil, errors.New(problem.Detail)
		}
		return nil, fmt.Errorf("the web app could not complete the sign-in (%s)", res.Status)
	}
	var grant Grant
	if err := json.Unmarshal(data, &grant); err != nil || !strings.HasPrefix(grant.Token, "orb_") {
		return nil, errors.New("the web app's answer had no token in it")
	}
	return &grant, nil
}

// APIBase is the grant's API with /v1, or "" when it is not one the CLI
// will send the token to: https only, except a loopback address (tests).
func (g *Grant) APIBase() string {
	u, err := url.Parse(strings.TrimRight(g.APIURL, "/"))
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ""
	}
	loopback := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return ""
	}
	base := u.Scheme + "://" + u.Host + strings.TrimSuffix(u.Path, "/v1")
	return base + "/v1"
}

// OpenBrowser opens target in the default browser.
func OpenBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "linux":
		cmd = exec.Command("xdg-open", target)
	default:
		return errors.New("no browser opener for " + runtime.GOOS)
	}
	return cmd.Start()
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// SanitizeHost keeps the characters the web app accepts in a token's name
// ([A-Za-z0-9 .@_-]), trimmed, at most 60.
func SanitizeHost(h string) string {
	var b strings.Builder
	for _, r := range h {
		if r == ' ' || r == '.' || r == '@' || r == '-' || r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 60 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSuffix(s, ".")
}

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%[1]s · Orbits CLI</title>
<style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;font:16px/1.5 system-ui,-apple-system,sans-serif;background:Canvas;color:CanvasText}main{max-width:28rem;padding:2rem}h1{font-size:1.5rem;margin:0 0 .5rem}p{margin:0;opacity:.7}</style></head>
<body><main><h1>%[1]s</h1><p>%[2]s</p></main></body></html>`, html.EscapeString(title), html.EscapeString(body))
}
