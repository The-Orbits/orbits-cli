package cli

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	orbits "github.com/The-Orbits/orbits-go"
)

func sseLine(n int, step, tone, text string) string {
	return "id: " + itoa(n) + "\nevent: line\ndata: {\"n\":" + itoa(n) + ",\"at\":\"2026-11-10T09:30:0" + itoa(n%10) + "Z\",\"step\":\"" + step + "\",\"tone\":\"" + tone + "\",\"text\":\"" + text + "\"}\n\n"
}

func itoa(n int) string { return strconv.Itoa(n) }

const sseSteps = "event: steps\ndata: [{\"key\":\"queued\",\"state\":\"done\",\"started_at\":\"2026-11-10T09:30:00Z\",\"finished_at\":\"2026-11-10T09:30:01Z\",\"seconds\":1},{\"key\":\"building\",\"state\":\"running\",\"started_at\":\"2026-11-10T09:30:01Z\",\"finished_at\":null,\"seconds\":null}]\n\n"

func streamEnding(state string) string {
	failure := "null"
	if state == "failed" {
		failure = `{"title":"Build failed","message":"npm run build exited with code 1","step":"building","line":42}`
	}
	return sseLine(1, "installing", "plain", "Installing dependencies") + sseSteps + sseLine(2, "building", "bad", "error: missing module") +
		"event: done\ndata: {\"state\":\"" + state + "\",\"failure\":" + failure + ",\"duration_seconds\":68}\n\n"
}

// uploadAPI answers an upload project's deploy: the project, the upload
// (to the fake's own /storage), its completion, the stream and the result.
func uploadAPI(t *testing.T, final string) (*fakeAPI, *httptest.Server) {
	f, srv := newFakeAPI(t)
	start := strings.Replace(fixture(t, "startSourceUpload.json"), "https://storage.example.com", srv.URL+"/storage", 1)
	f.on("GET "+projPath, 200, fixture(t, "getProject.upload.json")).
		on("POST "+projPath+"/uploads", 201, start).
		on("PUT /storage/sources/upl_test1.zip", 200, "").
		on("POST "+projPath+"/uploads/upl_test1/complete", 200, fixture(t, "completeSourceUpload.json")).
		on("GET "+projPath+"/deployments/dpl_test1/logs/stream", 200, streamEnding(final)).
		on("GET "+projPath+"/deployments/dpl_test1", 200, fixture(t, "getDeployment."+final+".json"))
	return f, srv
}

func writeSite(t *testing.T, dir string) {
	t.Helper()
	for name, body := range map[string]string{
		"index.html":            "<h1>hi</h1>",
		"assets/app.js":         "console.log(1)",
		".env.local":            "SECRET=1",
		".env.production.local": "SECRET=2",
		".env.production":       "PUBLIC=1",
		"node_modules/x/y.js":   "x",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

func TestDeployUploadsTheFolder(t *testing.T) {
	dir := linkHere(t)
	writeSite(t, dir)
	f, srv := uploadAPI(t, "ready")
	r := run(t, srv, "", "deploy")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.stderr)
	}
	if r.stdout != "https://web.theorbits.app\n" {
		t.Fatalf("stdout = %q, want only the address", r.stdout)
	}
	mustContain(t, "stderr", r.stderr, "Packed 3 files", "Uploaded", "Installing dependencies", "error: missing module", "✓ Queued", "Ready in 1:08", "dpl_test1")
	puts := f.sent("PUT", "/storage/sources/upl_test1.zip")
	if len(puts) != 1 {
		t.Fatalf("%d PUTs to storage", len(puts))
	}
	got := zipNames(t, puts[0].Body)
	want := []string{".env.production", "assets/app.js", "index.html"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("zip has %v, want %v (no .orbits, node_modules or .env*.local)", got, want)
	}
	if body := f.sentJSON("POST", projPath+"/uploads"); body["size"] != float64(len(puts[0].Body)) || body["filename"] != filepath.Base(dir)+".zip" {
		t.Fatalf("start upload body = %v", body)
	}
}

func TestDeployADifferentFolderAsJSON(t *testing.T) {
	dir := linkHere(t)
	writeSite(t, filepath.Join(dir, "dist"))
	_, srv := uploadAPI(t, "ready")
	r := run(t, srv, "", "deploy", "dist", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.stdout, `"state": "ready"`) || strings.Contains(r.stderr, "Installing dependencies") {
		t.Fatalf("stdout %q stderr %q", r.stdout, r.stderr)
	}
}

func TestDeployFailedBuildExitsFive(t *testing.T) {
	dir := linkHere(t)
	writeSite(t, dir)
	_, srv := uploadAPI(t, "failed")
	r := run(t, srv, "", "deploy", "--no-logs")
	if !IsBuildFailed(r.err) {
		t.Fatalf("err = %v, want a failed build", r.err)
	}
	var de *orbits.DeploymentError
	if !errors.As(r.err, &de) {
		t.Fatal("the SDK's DeploymentError is not in the chain")
	}
	mustContain(t, "error", r.err.Error(), "Build failed", "npm run build exited with code 1", "orbits logs dpl_test1")
	if strings.Contains(r.stderr, "Installing dependencies") || r.stdout != "" {
		t.Fatalf("--no-logs printed lines, or stdout has %q", r.stdout)
	}
}

func TestDeployNoWait(t *testing.T) {
	dir := linkHere(t)
	writeSite(t, dir)
	f, srv := uploadAPI(t, "ready")
	r := run(t, srv, "", "deploy", "--no-wait")
	if r.err != nil || r.stdout != "dpl_test1\n" {
		t.Fatalf("stdout %q, err %v", r.stdout, r.err)
	}
	if len(f.sent("GET", projPath+"/deployments/dpl_test1/logs/stream")) != 0 {
		t.Fatal("followed the build")
	}
}

func TestDeployGitProject(t *testing.T) {
	for _, c := range []struct {
		name       string
		args       []string
		wantBranch string
		wantErr    string
	}{
		{"--prod builds the production branch", []string{"--prod"}, "main", ""},
		{"--branch builds a preview", []string{"--branch", "feature/x"}, "feature/x", ""},
		{"nothing said, no terminal: explain", nil, "", "orbits deploy --prod"},
		{"a folder is refused", []string{"./dist"}, "", "builds from acme/web, not from a folder"},
		{"--prod with another branch", []string{"--prod", "--branch", "dev"}, "", "--prod deploys the production branch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			linkHere(t)
			f, srv := newFakeAPI(t)
			f.on("GET "+projPath, 200, fixture(t, "getProject.git.json")).
				on("POST "+projPath+"/deployments", 201, fixture(t, "createDeployment.json")).
				on("GET "+projPath+"/deployments/dpl_test1/logs/stream", 200, streamEnding("ready")).
				on("GET "+projPath+"/deployments/dpl_test1", 200, fixture(t, "getDeployment.ready.json"))
			r := run(t, srv, "", append([]string{"deploy"}, c.args...)...)
			if c.wantErr != "" {
				if r.err == nil || !strings.Contains(r.err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", r.err, c.wantErr)
				}
				if len(f.writes()) != 0 {
					t.Fatal("deployed anyway")
				}
				return
			}
			if r.err != nil {
				t.Fatal(r.err)
			}
			if got := f.sentJSON("POST", projPath+"/deployments"); got["branch"] != c.wantBranch {
				t.Fatalf("branch = %v, want %s", got["branch"], c.wantBranch)
			}
		})
	}
}

func TestDeployOutsideALinkedFolder(t *testing.T) {
	unlinkedHere(t)
	_, srv := newFakeAPI(t)
	r := run(t, srv, "", "deploy")
	if !errors.Is(r.err, errNoProject) {
		t.Fatalf("err = %v", r.err)
	}
}

func TestDeployWithProjectFlag(t *testing.T) {
	dir := unlinkedHere(t)
	writeSite(t, dir)
	f, srv := newFakeAPI(t)
	// Without a link, the team is the token's own, from GET /me.
	f.on("GET /me", 200, fixture(t, "getCurrentUser.json")).
		on("GET /teams/acme/projects/web", 200, fixture(t, "getProject.upload.json")).
		on("POST /teams/acme/projects/prj_test1/uploads", 201, strings.Replace(fixture(t, "startSourceUpload.json"), "https://storage.example.com", srv.URL+"/storage", 1)).
		on("PUT /storage/sources/upl_test1.zip", 200, "").
		on("POST /teams/acme/projects/prj_test1/uploads/upl_test1/complete", 200, fixture(t, "completeSourceUpload.json")).
		on("GET /teams/acme/projects/prj_test1/deployments/dpl_test1/logs/stream", 200, streamEnding("ready")).
		on("GET /teams/acme/projects/prj_test1/deployments/dpl_test1", 200, fixture(t, "getDeployment.ready.json"))
	r := run(t, srv, "", "--project", "web", "deploy")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.stderr)
	}
	if !strings.Contains(r.stderr, "https://orbits.africa/acme/web/deployments/dpl_test1") {
		t.Fatalf("no link to the deployment in %q", r.stderr)
	}
}
