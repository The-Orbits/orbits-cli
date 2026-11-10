package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	orbits "github.com/The-Orbits/orbits-go"
)

// fixture is a problem document from testdata/fixtures, copied from
// orbits-go's spec-checked fixtures.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "cli", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// apiError makes the SDK return the error a status and body give.
func apiError(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	zero := 0
	c, err := New("orb_live_test", Options{BaseURL: srv.URL + "/v1", Version: "v1.2.3", MaxRetries: &zero})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Projects.Get(context.Background(), "acme", "web")
	if err == nil {
		t.Fatal("no error")
	}
	return err
}

func TestExplain(t *testing.T) {
	cx := Context{Team: "acme", Login: "orbits login"}
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   []string
		not    []string
	}{
		{"unauthorized", 401, `{"status":401,"title":"Unauthorized","detail":"Invalid token","request_id":"req_1"}`,
			[]string{"refused the token", "Sign in again: orbits login", "request req_1"}, nil},
		{"read-only token", 403, fixture(t, "problem.read-only-token.json"),
			[]string{"this token is read-only", "Full access", "request req_test5"}, nil},
		{"route refused to tokens", 403, `{"status":403,"title":"Forbidden","detail":"API tokens can't manage integrations"}`,
			[]string{"API tokens can't manage integrations: API tokens can't do this", "https://orbits.africa/acme"}, nil},
		{"role", 403, `{"status":403,"title":"Forbidden","detail":"Viewers can't deploy."}`,
			[]string{"Viewers can't deploy.", "Your role"}, []string{"API tokens"}},
		{"not found", 404, fixture(t, "problem.not-found.json"),
			[]string{"Project not found", "not in team acme", "request req_test2"}, nil},
		{"plan", 402, fixture(t, "problem.plan-required.json"),
			[]string{"Functions need the Pro plan.", "pro plan", "plan_required"}, nil},
		{"validation", 422, fixture(t, "problem.validation.json"),
			[]string{"Validation failed", "name: must be 1 to 100 characters"}, nil},
		{"conflict", 409, fixture(t, "problem.upload-incomplete.json"),
			[]string{"The upload has not arrived yet.", "upload_incomplete"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := apiError(t, c.status, c.body)
			err := Explain(raw, cx)
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q lacks %q", err.Error(), w)
				}
			}
			for _, n := range c.not {
				if strings.Contains(err.Error(), n) {
					t.Errorf("%q has %q", err.Error(), n)
				}
			}
			if strings.Contains(err.Error(), "orbits: ") {
				t.Errorf("the SDK's prefix leaked: %q", err.Error())
			}
			e, ok := orbits.AsError(err)
			if !ok || e.Status != c.status {
				t.Fatalf("the *orbits.Error is not in the chain: %v", err)
			}
		})
	}
}

func TestExplainUnreachable(t *testing.T) {
	zero := 0
	c, err := New("orb_live_test", Options{BaseURL: "http://127.0.0.1:1/v1", Version: "dev", MaxRetries: &zero})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Me.Get(context.Background())
	err = Explain(err, Context{BaseURL: "http://127.0.0.1:1/v1"})
	if !strings.Contains(err.Error(), "could not reach http://127.0.0.1:1/v1") {
		t.Fatalf("err = %v", err)
	}
}

func TestExplainLeavesOthersAlone(t *testing.T) {
	plain := errors.New("no project linked")
	if Explain(plain, Context{}) != plain {
		t.Fatal("a CLI error was changed")
	}
	if Explain(nil, Context{}) != nil {
		t.Fatal("nil")
	}
	wrapped := fmt.Errorf("orbits: waiting: %w", context.Canceled)
	if got := Explain(wrapped, Context{}); !errors.Is(got, context.Canceled) {
		t.Fatalf("a cancelled call must stay one: %v", got)
	}
	if got := Explain(errors.New("orbits: not a folder: x"), Context{}); got.Error() != "not a folder: x" {
		t.Fatalf("got %q", got)
	}
}

func TestUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"id":"usr_1","email":"a@example.com","name":"A","teams":[]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New("orb_live_test", Options{BaseURL: srv.URL + "/v1", Version: "v0.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Me.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ua, "orbits-cli/0.3.0 orbits-go/") {
		t.Fatalf("User-Agent = %q", ua)
	}
}

func TestNewRefusesAnEmptyToken(t *testing.T) {
	if _, err := New("", Options{}); err == nil || strings.HasPrefix(err.Error(), "orbits: ") {
		t.Fatalf("err = %v", err)
	}
}
