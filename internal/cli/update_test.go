package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/The-Orbits/orbits-cli/internal/update"
)

func fakeReleases(t *testing.T, latest string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/"+latest, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	old := update.BaseURL
	update.BaseURL = srv.URL + "/releases"
	t.Cleanup(func() { update.BaseURL = old })
}

func withVersion(t *testing.T, v string) {
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func TestUpdateCheck(t *testing.T) {
	fakeReleases(t, "v0.2.0")
	withVersion(t, "v0.1.0")
	r := runWith(t, nil, opts{noToken: true}, "update", "--check")
	if r.err != nil || r.stdout != "v0.2.0\n" || !strings.Contains(r.stderr, "orbits v0.2.0 is out (you have v0.1.0)") {
		t.Fatalf("stdout %q stderr %q err %v", r.stdout, r.stderr, r.err)
	}
	withVersion(t, "v0.2.0")
	r = runWith(t, nil, opts{noToken: true}, "update")
	if r.err != nil || !strings.Contains(r.stderr, "is the latest version") {
		t.Fatalf("stderr %q err %v", r.stderr, r.err)
	}
}

func TestUpdateFromADevBuildNeedsAVersion(t *testing.T) {
	fakeReleases(t, "v0.2.0")
	withVersion(t, "dev")
	r := runWith(t, nil, opts{noToken: true}, "update")
	if r.err == nil || !strings.Contains(r.err.Error(), "orbits update v0.2.0") {
		t.Fatalf("err = %v", r.err)
	}
}
