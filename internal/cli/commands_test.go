package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cmdCase is one run of the CLI against canned routes: what it must send
// and print.
type cmdCase struct {
	name   string
	args   []string
	stdin  string
	routes [][3]string // method+path, status, fixture file or literal body (starting with { or [)
	// wantSent names a write that must happen, with its JSON body if set.
	wantSent string
	wantBody map[string]any
	// wantQuery is the query the first GET of wantGet carried.
	wantGet, wantQuery string
	stdout             []string
	notStdout          []string
	stderr             []string
	wantErr            string
}

func body(t *testing.T, s string) string {
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") || strings.HasPrefix(s, "id:") || strings.HasPrefix(s, "event:") || s == "" {
		return s
	}
	return fixture(t, s)
}

func runCases(t *testing.T, cases []cmdCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			linkHere(t)
			f, srv := newFakeAPI(t)
			for _, r := range c.routes {
				status := 200
				switch r[1] {
				case "201":
					status = 201
				case "202":
					status = 202
				case "204":
					status = 204
				case "403":
					status = 403
				case "404":
					status = 404
				case "409":
					status = 409
				}
				f.on(r[0], status, body(t, r[2]))
			}
			res := run(t, srv, c.stdin, c.args...)
			if c.wantErr != "" {
				if res.err == nil || !strings.Contains(res.err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", res.err, c.wantErr)
				}
			} else if res.err != nil {
				t.Fatalf("err = %v\nstderr: %s", res.err, res.stderr)
			}
			if c.wantSent != "" {
				method, path, _ := strings.Cut(c.wantSent, " ")
				if c.wantBody != nil {
					if got := f.sentJSON(method, path); !reflect.DeepEqual(got, c.wantBody) {
						t.Fatalf("body = %v\nwant   %v", got, c.wantBody)
					}
				} else if len(f.sent(method, path)) != 1 {
					t.Fatalf("%s was not sent once: %+v", c.wantSent, f.writes())
				}
			}
			if c.wantGet != "" {
				method, path, _ := strings.Cut(c.wantGet, " ")
				gets := f.sent(method, path)
				if len(gets) == 0 || gets[0].Query != c.wantQuery {
					t.Fatalf("%s asked with %+v, want query %q", c.wantGet, gets, c.wantQuery)
				}
			}
			mustContain(t, "stdout", res.stdout, c.stdout...)
			mustContain(t, "stderr", res.stderr, c.stderr...)
			for _, n := range c.notStdout {
				if strings.Contains(res.stdout, n) {
					t.Fatalf("stdout has %q:\n%s", n, res.stdout)
				}
			}
		})
	}
}

const teams = "/teams/" + teamID

func TestProjects(t *testing.T) {
	runCases(t, []cmdCase{
		{name: "list", args: []string{"projects", "list"},
			routes:  [][3]string{{"GET " + teams + "/projects", "200", "listProjects.page1.json"}, {"GET " + teams + "/projects", "200", "listProjects.page2.json"}},
			wantGet: "GET " + teams + "/projects", wantQuery: "limit=100",
			stdout: []string{"PROJECT", "one", "two", "three", "upload"}},
		{name: "list ids", args: []string{"projects", "list", "-q", "--limit", "2"},
			routes: [][3]string{{"GET " + teams + "/projects", "200", "listProjects.page1.json"}},
			stdout: []string{"one\ntwo\n"}, notStdout: []string{"three"}},
		{name: "list json", args: []string{"projects", "list", "--json"},
			routes: [][3]string{{"GET " + teams + "/projects", "200", "listProjects.page2.json"}},
			stdout: []string{`"slug": "three"`, `"source_kind": "upload"`}},
		{name: "get the linked one", args: []string{"projects", "get"},
			routes: [][3]string{{"GET " + projPath, "200", "getProject.upload.json"}},
			stdout: []string{"web", "https://web.theorbits.app", "static", "upload"}},
		{name: "get by slug", args: []string{"projects", "get", "web"},
			routes: [][3]string{{"GET " + teams + "/projects/web", "200", "getProject.git.json"}},
			stdout: []string{"acme/web · main"}},
		{name: "get missing", args: []string{"projects", "get", "nope"},
			routes:  [][3]string{{"GET " + teams + "/projects/nope", "404", "problem.not-found.json"}},
			wantErr: "Project not found"},
		{name: "create upload", args: []string{"projects", "create", "web", "--workload", "static", "--output-dir", "dist", "--spa"},
			routes:   [][3]string{{"POST " + teams + "/projects", "201", "getProject.upload.json"}},
			wantSent: "POST " + teams + "/projects",
			wantBody: map[string]any{"name": "web", "source_kind": "upload", "workload": map[string]any{"type": "static", "output_directory": "dist", "spa_fallback": true}},
			stderr:   []string{"Made project", "orbits link web"}},
		{name: "create from a repository", args: []string{"projects", "create", "api", "--repo", "acme/api", "--branch", "trunk"},
			routes:   [][3]string{{"POST " + teams + "/projects", "201", "getProject.git.json"}},
			wantSent: "POST " + teams + "/projects",
			wantBody: map[string]any{"name": "api", "source": map[string]any{"provider": "github", "repository": "acme/api", "production_branch": "trunk"}}},
		{name: "create: --spa without static", args: []string{"projects", "create", "api", "--spa"}, wantErr: "--output-dir and --spa are for --workload static"},
		{name: "create: bad workload", args: []string{"projects", "create", "api", "--workload", "vm"}, wantErr: "--workload must be one of"},
		{name: "delete waits for the operation", args: []string{"projects", "delete", "web", "--yes"},
			routes: [][3]string{
				{"GET " + teams + "/projects/web", "200", "getProject.upload.json"},
				{"GET " + teams + "/projects/prj_test1/deletion-impact", "200", "getProjectDeletionImpact.json"},
				{"DELETE " + teams + "/projects/prj_test1", "202", "getOperation.running.json"},
				{"GET /operations/op_test1", "200", "getOperation.running.json"},
				{"GET /operations/op_test1", "200", "getOperation.succeeded.json"},
			},
			wantSent: "DELETE " + teams + "/projects/prj_test1",
			stderr:   []string{"Deleted web", "still linked"}},
		{name: "delete that fails", args: []string{"projects", "delete", "web", "--yes"},
			routes: [][3]string{
				{"GET " + teams + "/projects/web", "200", "getProject.upload.json"},
				{"GET " + teams + "/projects/prj_test1/deletion-impact", "200", "getProjectDeletionImpact.json"},
				{"DELETE " + teams + "/projects/prj_test1", "202", "getOperation.running.json"},
				{"GET /operations/op_test1", "200", "getOperation.failed.json"},
			},
			wantErr: "domains could not be detached"},
		{name: "pause", args: []string{"projects", "pause", "--yes"},
			routes:   [][3]string{{"POST " + projPath + "/pause", "200", "pauseProject.json"}},
			wantSent: "POST " + projPath + "/pause", wantBody: map[string]any{"paused": true},
			stderr: []string{"Paused web"}},
		{name: "resume", args: []string{"projects", "resume"},
			routes:   [][3]string{{"POST " + projPath + "/pause", "200", "getProject.upload.json"}},
			wantSent: "POST " + projPath + "/pause", wantBody: map[string]any{"paused": false},
			stderr: []string{"Resumed web"}},
	})
}

func TestDeployments(t *testing.T) {
	dpl := projPath + "/deployments"
	runCases(t, []cmdCase{
		{name: "list", args: []string{"deployments", "list", "--environment", "production"},
			routes:  [][3]string{{"GET " + dpl, "200", "listProjectDeployments.json"}},
			wantGet: "GET " + dpl, wantQuery: "environment=production&limit=20",
			stdout: []string{"dpl_test1", "dpl_test0", "(current)", "Uploaded web.zip"}},
		{name: "list: bad state", args: []string{"deployments", "list", "--state", "done"}, wantErr: "--state must be one of"},
		{name: "get the newest", args: []string{"deployments", "get"},
			routes: [][3]string{{"GET " + dpl, "200", "listProjectDeployments.json"}, {"GET " + dpl + "/dpl_test1", "200", "getDeployment.ready.json"}},
			stdout: []string{"https://web-0000000.theorbits.app", "web.theorbits.app", "Going live"}},
		{name: "get a failed one", args: []string{"deployments", "get", "dpl_test1"},
			routes: [][3]string{{"GET " + dpl + "/dpl_test1", "200", "getDeployment.failed.json"}},
			stdout: []string{"Build failed: npm run build exited with code 1"}},
		{name: "promote follows the build", args: []string{"deployments", "promote", "dpl_test0", "--yes"},
			routes: [][3]string{
				{"POST " + dpl + "/dpl_test0/promote", "202", "completeSourceUpload.json"},
				{"GET " + projPath, "200", "getProject.upload.json"},
				{"GET " + dpl + "/dpl_test1/logs/stream", "200", streamEnding("ready")},
				{"GET " + dpl + "/dpl_test1", "200", "getDeployment.ready.json"},
			},
			wantSent: "POST " + dpl + "/dpl_test0/promote",
			stdout:   []string{"https://web.theorbits.app"}},
		{name: "rollback to the one before", args: []string{"deployments", "rollback", "--yes"},
			routes: [][3]string{
				{"GET " + dpl, "200", "listProjectDeployments.json"},
				{"POST " + dpl + "/dpl_test0/rollback", "200", "rollbackDeployment.json"},
			},
			wantGet: "GET " + dpl, wantQuery: "environment=production&limit=20&live=true",
			wantSent: "POST " + dpl + "/dpl_test0/rollback",
			stderr:   []string{"Production now serves dpl_test0", "orbits deployments rollback dpl_test1"}},
		{name: "rollback to what is live", args: []string{"deployments", "rollback", "dpl_test1", "--yes"},
			routes:  [][3]string{{"GET " + dpl, "200", "listProjectDeployments.json"}},
			wantErr: "what production serves already"},
		{name: "redeploy production without cache", args: []string{"deployments", "redeploy", "--no-cache", "--no-wait"},
			routes: [][3]string{
				{"GET " + dpl, "200", "listProjectDeployments.json"},
				{"POST " + dpl + "/dpl_test1/redeploy", "202", "completeSourceUpload.json"},
				{"GET " + projPath, "200", "getProject.upload.json"},
			},
			wantSent: "POST " + dpl + "/dpl_test1/redeploy", wantBody: map[string]any{"use_cache": false},
			stdout: []string{"dpl_test1"}},
		{name: "cancel", args: []string{"deployments", "cancel", "dpl_test1", "--yes"},
			routes:   [][3]string{{"POST " + dpl + "/dpl_test1/cancel", "200", "cancelDeployment.json"}},
			wantSent: "POST " + dpl + "/dpl_test1/cancel",
			stderr:   []string{"Canceled dpl_test1"}},
	})
}

func TestLogs(t *testing.T) {
	dpl := projPath + "/deployments"
	runCases(t, []cmdCase{
		{name: "a finished build's log, page by page", args: []string{"logs", "dpl_test1"},
			routes: [][3]string{{"GET " + dpl + "/dpl_test1/logs", "200", "listDeploymentLogs.page1.json"}, {"GET " + dpl + "/dpl_test1/logs", "200", "listDeploymentLogs.page2.json"}},
			stdout: []string{"Installing dependencies\nvite build\nBuilt in 1.2s\n"}},
		{name: "the newest deployment's", args: []string{"logs"},
			routes: [][3]string{{"GET " + dpl, "200", "listProjectDeployments.json"}, {"GET " + dpl + "/dpl_test1/logs", "200", "listDeploymentLogs.page2.json"}},
			stdout: []string{"Built in 1.2s"}},
		{name: "follow", args: []string{"logs", "dpl_test1", "-f"},
			routes: [][3]string{{"GET " + dpl + "/dpl_test1/logs/stream", "200", streamEnding("ready")}},
			stdout: []string{"Installing dependencies\nerror: missing module\n"}},
		{name: "follow a build that fails", args: []string{"logs", "dpl_test1", "-f"},
			routes:  [][3]string{{"GET " + dpl + "/dpl_test1/logs/stream", "200", streamEnding("failed")}, {"GET " + dpl + "/dpl_test1", "200", "getDeployment.failed.json"}},
			wantErr: "Build failed"},
		{name: "runtime, oldest first", args: []string{"logs", "--runtime", "--since", "24h", "--level", "error"},
			routes:  [][3]string{{"GET " + projPath + "/logs", "200", "listRuntimeLogs.json"}},
			wantGet: "GET " + projPath + "/logs", wantQuery: "level=error&limit=100&range=24h",
			stdout: []string{"cart loaded", "GET /checkout 500 41ms"}},
		{name: "runtime: a bad range", args: []string{"logs", "--runtime", "--since", "1y"}, wantErr: "--since must be one of"},
		{name: "build filters are for runtime", args: []string{"logs", "--level", "error"}, wantErr: "are for --runtime"},
	})
}

func TestRuntimeLogsComeOutInOrder(t *testing.T) {
	linkHere(t)
	f, srv := newFakeAPI(t)
	f.on("GET "+projPath+"/logs", 200, fixture(t, "listRuntimeLogs.json"))
	r := run(t, srv, "", "logs", "--runtime")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if strings.Index(r.stdout, "cart loaded") > strings.Index(r.stdout, "/checkout") {
		t.Fatalf("not oldest first:\n%s", r.stdout)
	}
}

func TestEnv(t *testing.T) {
	env := projPath + "/env"
	runCases(t, []cmdCase{
		{name: "ls hides values", args: []string{"env", "ls"},
			routes: [][3]string{{"GET " + env, "200", "listEnvVars.json"}},
			stdout: []string{"NEXT_PUBLIC_SITE_NAME", "DATABASE_URL", "sensitive"}, notStdout: []string{"Storefront", "VALUE"}},
		{name: "ls --json hides values too", args: []string{"env", "ls", "--json"},
			routes: [][3]string{{"GET " + env, "200", "listEnvVars.json"}},
			stdout: []string{`"value": null`}, notStdout: []string{"Storefront"}},
		{name: "ls --show-values reveals plain ones", args: []string{"env", "ls", "--show-values"},
			routes:   [][3]string{{"GET " + env, "200", "listEnvVars.json"}, {"POST " + env + "/LOG_LEVEL/reveal", "200", "revealEnvVar.json"}},
			wantSent: "POST " + env + "/LOG_LEVEL/reveal",
			stdout:   []string{"Storefront", "debug", "(write-only)"}},
		{name: "add from stdin", args: []string{"env", "add", "API_URL", "--environment", "production,preview"}, stdin: "https://api.example.com\n",
			routes:   [][3]string{{"POST " + env, "201", "createEnvVars.json"}},
			wantSent: "POST " + env,
			wantBody: map[string]any{"variables": []any{map[string]any{"key": "API_URL", "value": "https://api.example.com", "environments": []any{"production", "preview"}, "sensitive": true}}},
			stderr:   []string{"Added API_URL", "orbits deployments redeploy"}},
		{name: "add plain, replacing", args: []string{"env", "add", "SITE", "Storefront", "--plain", "--force"},
			routes:   [][3]string{{"POST " + env, "201", "createEnvVars.json"}},
			wantSent: "POST " + env,
			wantBody: map[string]any{"variables": []any{map[string]any{"key": "SITE", "value": "Storefront", "sensitive": false}}, "on_conflict": "replace"}},
		{name: "add one that exists", args: []string{"env", "add", "SITE", "x"},
			routes:  [][3]string{{"POST " + env, "409", `{"status":409,"title":"Conflict","detail":"SITE already exists."}`}},
			wantErr: "replace it with --force"},
		{name: "add: bad environment", args: []string{"env", "add", "SITE", "x", "--environment", "staging"}, wantErr: "--environment must be one of"},
		{name: "rm", args: []string{"env", "rm", "API_URL", "--yes"},
			routes:   [][3]string{{"DELETE " + env + "/API_URL", "200", "deleteEnvVar.json"}},
			wantSent: "DELETE " + env + "/API_URL",
			stderr:   []string{"Removed API_URL", "orbits env restore API_URL"}},
		{name: "restore", args: []string{"env", "restore", "API_URL"},
			routes:   [][3]string{{"POST " + env + "/API_URL/restore", "200", "restoreEnvVar.json"}},
			wantSent: "POST " + env + "/API_URL/restore",
			stderr:   []string{"Restored API_URL"}},
		{name: "pull with a read-only token", args: []string{"env", "pull"},
			routes:  [][3]string{{"POST " + env + "/pull", "403", "problem.read-only-token.json"}},
			wantErr: "this token is read-only"},
	})
}

func TestEnvPullWritesAPrivateFile(t *testing.T) {
	dir := linkHere(t)
	f, srv := newFakeAPI(t)
	f.on("POST "+projPath+"/env/pull", 200, fixture(t, "pullEnvVars.json"))
	old := gitIgnored
	t.Cleanup(func() { gitIgnored = old })
	gitIgnored = func(_ context.Context, path string) (bool, bool) { return false, true }

	r := run(t, srv, "", "env", "pull")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := f.sentJSON("POST", projPath+"/env/pull"); got["environment"] != "development" {
		t.Fatalf("pull body = %v", got)
	}
	p := filepath.Join(dir, ".env.local")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(p)
	want := "GREETING=\"hello \\\"world\\\"\\nline two \\$HOME\"\nLOG_LEVEL=\"debug\"\n"
	if !strings.HasSuffix(string(data), want) || !strings.HasPrefix(string(data), "# Written by orbits env pull") {
		t.Fatalf("file:\n%s\nwant it to end with:\n%s", data, want)
	}
	mustContain(t, "stderr", r.stderr, "Wrote 2 variables", "DATABASE_URL", "is not ignored by Git")
	if strings.Contains(r.stdout+r.stderr, "debug") {
		t.Fatal("a value was printed")
	}

	// It is there now: replacing it asks, and with no terminal, refuses.
	r = run(t, srv, "", "env", "pull")
	if r.err == nil || !strings.Contains(r.err.Error(), "Replace .env.local? Pass --yes") {
		t.Fatalf("err = %v", r.err)
	}
	if len(f.sent("POST", projPath+"/env/pull")) != 1 {
		t.Fatal("pulled again before asking")
	}
	r = run(t, srv, "", "env", "pull", "--yes", ".env.local")
	if r.err != nil {
		t.Fatal(r.err)
	}
}

func TestEnvPullRefusesASymlink(t *testing.T) {
	dir := linkHere(t)
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(dir, ".env.local")); err != nil {
		t.Skip("no symlinks")
	}
	f, srv := newFakeAPI(t)
	f.on("POST "+projPath+"/env/pull", 200, fixture(t, "pullEnvVars.json"))
	r := run(t, srv, "", "env", "pull", "--yes")
	if r.err == nil || !strings.Contains(r.err.Error(), "symbolic link") {
		t.Fatalf("err = %v", r.err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("wrote through the link")
	}
}

func TestEnvPullJSONListsKeysNotValues(t *testing.T) {
	linkHere(t)
	f, srv := newFakeAPI(t)
	f.on("POST "+projPath+"/env/pull", 200, fixture(t, "pullEnvVars.json"))
	old := gitIgnored
	t.Cleanup(func() { gitIgnored = old })
	gitIgnored = func(context.Context, string) (bool, bool) { return true, true }
	r := run(t, srv, "", "env", "pull", "out.env", "--environment", "production", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got["file"] != "out.env" || strings.Contains(r.stdout, "debug") {
		t.Fatalf("stdout %s", r.stdout)
	}
}

func TestDomains(t *testing.T) {
	d := teams + "/domains"
	runCases(t, []cmdCase{
		{name: "ls: the linked project's", args: []string{"domains", "ls"},
			routes:  [][3]string{{"GET " + d, "200", "listDomains.json"}},
			wantGet: "GET " + d, wantQuery: "project=" + projectID,
			stdout: []string{"shop.example.com", "pending", "web"}},
		{name: "ls --all", args: []string{"domains", "ls", "--all"},
			routes:  [][3]string{{"GET " + d, "200", "listDomains.json"}},
			wantGet: "GET " + d, wantQuery: ""},
		{name: "add to the project", args: []string{"domains", "add", "shop.example.com"},
			routes:   [][3]string{{"POST " + d, "201", "addDomain.json"}},
			wantSent: "POST " + d, wantBody: map[string]any{"domain": "shop.example.com", "project": projectID},
			stdout: []string{"CNAME", "cname.theorbits.app"}, stderr: []string{"Added shop.example.com", "orbits domains verify"}},
		{name: "add a redirect on Orbits DNS", args: []string{"domains", "add", "www.example.com", "--redirect-to", "example.com", "--dns", "orbits"},
			routes:   [][3]string{{"POST " + d, "201", "addDomain.json"}},
			wantSent: "POST " + d, wantBody: map[string]any{"domain": "www.example.com", "to": "example.com", "dns": "orbits"}},
		{name: "add a branch's", args: []string{"domains", "add", "staging.example.com", "--branch", "staging"},
			routes:   [][3]string{{"POST " + d, "201", "addDomain.json"}},
			wantSent: "POST " + d, wantBody: map[string]any{"domain": "staging.example.com", "project": projectID, "branch": "staging"}},
		{name: "verify", args: []string{"domains", "verify", "shop.example.com"},
			routes:   [][3]string{{"POST " + d + "/shop.example.com/checks", "200", "checkDomain.json"}},
			wantSent: "POST " + d + "/shop.example.com/checks",
			stdout:   []string{"shop.example.com", "valid", "active, until"}, stderr: []string{"DNS is set up"}},
		{name: "rm", args: []string{"domains", "rm", "shop.example.com", "--yes"},
			routes:   [][3]string{{"DELETE " + d + "/shop.example.com", "204", ""}},
			wantSent: "DELETE " + d + "/shop.example.com", stderr: []string{"Removed shop.example.com"}},
		{name: "records ls", args: []string{"domains", "records", "ls", "example.com"},
			routes: [][3]string{{"GET " + d + "/example.com/records", "200", "listDnsRecords.json"}},
			stdout: []string{"rec_test1", "203.0.113.10", "auto"}},
		{name: "records add", args: []string{"domains", "records", "add", "example.com", "txt", "_verify", "token-value", "--ttl", "300"},
			routes:   [][3]string{{"POST " + d + "/example.com/records", "201", "addDnsRecord.json"}},
			wantSent: "POST " + d + "/example.com/records", wantBody: map[string]any{"type": "TXT", "name": "_verify", "value": "token-value", "ttl": 300.0}},
		{name: "records add: bad ttl", args: []string{"domains", "records", "add", "example.com", "A", "@", "203.0.113.10", "--ttl", "5"}, wantErr: "--ttl must be"},
		{name: "records rm", args: []string{"domains", "records", "rm", "example.com", "rec_test1", "--yes"},
			routes:   [][3]string{{"DELETE " + d + "/example.com/records/rec_test1", "204", ""}},
			wantSent: "DELETE " + d + "/example.com/records/rec_test1"},
	})
}

func TestFlagsWorkflowsSandboxesAlerts(t *testing.T) {
	runCases(t, []cmdCase{
		{name: "flags ls", args: []string{"flags", "ls"},
			routes: [][3]string{{"GET " + projPath + "/flags", "200", "listFlags.json"}},
			stdout: []string{"new-checkout", "on 25%", "off"}},
		{name: "flags create", args: []string{"flags", "create", "banner", "--type", "string", "--description", "Top banner"},
			routes:   [][3]string{{"POST " + projPath + "/flags", "201", "createFlag.json"}},
			wantSent: "POST " + projPath + "/flags", wantBody: map[string]any{"key": "banner", "type": "string", "description": "Top banner"}},
		{name: "flags set in production asks", args: []string{"flags", "set", "new-checkout", "--environment", "production", "--off"},
			wantErr: "Pass --yes"},
		{name: "flags set", args: []string{"flags", "set", "new-checkout", "--environment", "production", "--off", "--rollout", "25", "--yes"},
			routes:   [][3]string{{"PUT " + projPath + "/flags/new-checkout/environments/production", "200", "setFlagEnvironment.json"}},
			wantSent: "PUT " + projPath + "/flags/new-checkout/environments/production", wantBody: map[string]any{"on": false, "rollout": 25.0},
			stderr: []string{"new-checkout in production: off"}},
		{name: "flags set: nothing to change", args: []string{"flags", "set", "x", "--environment", "preview"}, wantErr: "say what to change"},
		{name: "flags rules add", args: []string{"flags", "rules", "add", "new-checkout", "--environment", "preview", "--attribute", "email", "--operator", "ends_with", "--values", "@example.com", "--serve", "true"},
			routes:   [][3]string{{"POST " + projPath + "/flags/new-checkout/environments/preview/rules", "201", "setFlagEnvironment.json"}},
			wantSent: "POST " + projPath + "/flags/new-checkout/environments/preview/rules",
			wantBody: map[string]any{"attribute": "email", "operator": "ends_with", "values": []any{"@example.com"}, "serve": true}},
		{name: "workflows ls", args: []string{"workflows", "ls"},
			routes: [][3]string{{"GET " + projPath + "/workflows", "200", "getWorkflows.json"}},
			stdout: []string{"send-digest", "Mondays at 08:00", "98%"}},
		{name: "workflows runs", args: []string{"workflows", "runs"},
			routes: [][3]string{{"GET " + projPath + "/workflows", "200", "getWorkflows.json"}},
			stdout: []string{"run_test1", "running"}},
		{name: "workflows run", args: []string{"workflows", "run", "send-digest", "--input", `{"week":45}`},
			routes:   [][3]string{{"POST " + projPath + "/workflows/send-digest/runs", "201", "runWorkflow.json"}},
			wantSent: "POST " + projPath + "/workflows/send-digest/runs", wantBody: map[string]any{"input": map[string]any{"week": 45.0}},
			stderr: []string{"Started send-digest"}},
		{name: "workflows run: bad input", args: []string{"workflows", "run", "x", "--input", "{"}, wantErr: "--input must be JSON"},
		{name: "sandboxes ls", args: []string{"sandboxes", "ls"},
			routes: [][3]string{{"GET " + projPath + "/sandboxes", "200", "listSandboxes.json"}},
			stdout: []string{"try-upgrade", "running", "node22"}},
		{name: "sandboxes create", args: []string{"sandboxes", "create", "try-upgrade", "--clone"},
			routes:   [][3]string{{"POST " + projPath + "/sandboxes", "201", "createSandbox.json"}},
			wantSent: "POST " + projPath + "/sandboxes",
			wantBody: map[string]any{"name": "try-upgrade", "runtime": "node22", "size": "small", "clone": true, "env": false, "stop_after": 30.0}},
		{name: "sandboxes rm", args: []string{"sandboxes", "rm", "try-upgrade", "--yes"},
			routes:   [][3]string{{"DELETE " + projPath + "/sandboxes/try-upgrade", "204", ""}},
			wantSent: "DELETE " + projPath + "/sandboxes/try-upgrade"},
		{name: "alerts ls", args: []string{"alerts", "ls"},
			routes: [][3]string{{"GET " + projPath + "/alerts", "200", "listAlerts.json"}},
			stdout: []string{"alr_test1", "error-rate above 5% over 5 min"}},
		{name: "alerts create", args: []string{"alerts", "create", "Errors", "--metric", "error-rate", "--threshold", "5"},
			routes:   [][3]string{{"POST " + projPath + "/alerts", "201", "createAlert.json"}},
			wantSent: "POST " + projPath + "/alerts",
			wantBody: map[string]any{"name": "Errors", "metric": "error-rate", "direction": "above", "threshold": 5.0, "window_minutes": 5.0, "channels": []any{"email"}}},
		{name: "alerts disable", args: []string{"alerts", "disable", "alr_test1"},
			routes:   [][3]string{{"PATCH " + projPath + "/alerts/alr_test1", "200", "createAlert.json"}},
			wantSent: "PATCH " + projPath + "/alerts/alr_test1", wantBody: map[string]any{"on": false}},
		{name: "usage", args: []string{"usage"},
			routes: [][3]string{{"GET " + teams + "/usage", "200", "getUsage.json"}},
			stdout: []string{"123,456", "50 MB", "12.5"}},
		{name: "activity", args: []string{"activity"},
			routes:  [][3]string{{"GET " + teams + "/activity", "200", "listActivity.json"}},
			wantGet: "GET " + teams + "/activity", wantQuery: "limit=50&project=" + projectID,
			stdout: []string{"Test User deployed web to production"}},
	})
}

// Every command that deletes something or changes production asks first;
// with no terminal and no --yes it refuses, and sends nothing.
func TestDestructiveCommandsAsk(t *testing.T) {
	for _, args := range [][]string{
		{"projects", "delete", "web"},
		{"projects", "pause"},
		{"deployments", "promote", "dpl_test0"},
		{"deployments", "rollback", "dpl_test0"},
		{"deployments", "cancel", "dpl_test1"},
		{"env", "rm", "API_URL"},
		{"domains", "rm", "shop.example.com"},
		{"domains", "records", "rm", "example.com", "rec_test1"},
		{"flags", "rm", "new-checkout"},
		{"flags", "rules", "rm", "new-checkout", "rule_1", "--environment", "production"},
		{"sandboxes", "rm", "try-upgrade"},
		{"alerts", "rm", "alr_test1"},
		{"workflows", "cancel", "run_test1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			linkHere(t)
			f, srv := newFakeAPI(t)
			f.on("GET "+teams+"/projects/web", 200, fixture(t, "getProject.upload.json")).
				on("GET "+teams+"/projects/prj_test1/deletion-impact", 200, fixture(t, "getProjectDeletionImpact.json")).
				on("GET "+projPath+"/deployments", 200, fixture(t, "listProjectDeployments.json"))
			r := run(t, srv, "", args...)
			if r.err == nil || !strings.Contains(r.err.Error(), "Pass --yes") {
				t.Fatalf("err = %v, want a refusal naming --yes", r.err)
			}
			if w := f.writes(); len(w) != 0 {
				t.Fatalf("sent %+v before asking", w)
			}
		})
	}
}

func TestConfirmAtATerminal(t *testing.T) {
	for answer, wantDelete := range map[string]bool{"y\n": true, "yes\n": true, "n\n": false, "\n": false} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			linkHere(t)
			f, srv := newFakeAPI(t)
			f.on("DELETE "+projPath+"/env/API_URL", 200, fixture(t, "deleteEnvVar.json"))
			r := runWith(t, srv, opts{stdin: answer, interactive: true}, "env", "rm", "API_URL")
			if !strings.Contains(r.stderr, "Remove API_URL from prj_test1? [y/N]") {
				t.Fatalf("no question: %q", r.stderr)
			}
			sent := len(f.sent("DELETE", projPath+"/env/API_URL")) == 1
			if sent != wantDelete {
				t.Fatalf("answer %q: deleted = %v", answer, sent)
			}
			if !wantDelete && (r.err == nil || r.err.Error() != "cancelled") {
				t.Fatalf("err = %v, want cancelled", r.err)
			}
		})
	}
}

// The API's refusals of a token read as what to do.
func TestTokenRefusals(t *testing.T) {
	runCases(t, []cmdCase{
		{name: "read-only token", args: []string{"env", "add", "A", "b"},
			routes:  [][3]string{{"POST " + projPath + "/env", "403", "problem.read-only-token.json"}},
			wantErr: "Sign in again with full access"},
		{name: "a route tokens can't use", args: []string{"projects", "get"},
			routes:  [][3]string{{"GET " + projPath, "403", `{"status":403,"title":"Forbidden","detail":"API tokens can't use this endpoint"}`}},
			wantErr: "Do it in the web app instead: https://orbits.africa/acme"},
		{name: "not in the token's team", args: []string{"projects", "get"},
			routes:  [][3]string{{"GET " + projPath, "404", "problem.not-found.json"}},
			wantErr: "not in team acme"},
	})
}

func TestMoreSubcommands(t *testing.T) {
	runCases(t, []cmdCase{
		{name: "flags get", args: []string{"flags", "get", "new-checkout"},
			routes: [][3]string{{"GET " + projPath + "/flags/new-checkout", "200", "getFlag.json"}},
			stdout: []string{"new-checkout", "on 25%", "rule rule_test1", "email ends_with @example.com", "Set Production rollout to 25%"}},
		{name: "flags rules rm", args: []string{"flags", "rules", "rm", "new-checkout", "rule_test1", "--environment", "production", "--yes"},
			routes:   [][3]string{{"DELETE " + projPath + "/flags/new-checkout/environments/production/rules/rule_test1", "204", ""}},
			wantSent: "DELETE " + projPath + "/flags/new-checkout/environments/production/rules/rule_test1"},
		{name: "flags rm", args: []string{"flags", "rm", "new-checkout", "--yes"},
			routes:   [][3]string{{"DELETE " + projPath + "/flags/new-checkout", "204", ""}},
			wantSent: "DELETE " + projPath + "/flags/new-checkout"},
		{name: "workflows runs <id>", args: []string{"workflows", "runs", "run_test1"},
			routes: [][3]string{{"GET " + projPath + "/workflows/runs/run_test1", "200", "getWorkflowRun.json"}},
			stdout: []string{"send-digest", "failed", "inventory service timed out"}},
		{name: "workflows retry", args: []string{"workflows", "retry", "run_test1"},
			routes:   [][3]string{{"POST " + projPath + "/workflows/runs/run_test1/retry", "200", "retryWorkflowRun.json"}},
			wantSent: "POST " + projPath + "/workflows/runs/run_test1/retry", stderr: []string{"run_test1: running"}},
		{name: "workflows cancel", args: []string{"workflows", "cancel", "run_test1", "--yes"},
			routes:   [][3]string{{"POST " + projPath + "/workflows/runs/run_test1/cancel", "200", "getWorkflowRun.json"}},
			wantSent: "POST " + projPath + "/workflows/runs/run_test1/cancel"},
		{name: "sandboxes stop", args: []string{"sandboxes", "stop", "try-upgrade"},
			routes:   [][3]string{{"POST " + projPath + "/sandboxes/try-upgrade/stop", "200", "stopSandbox.json"}},
			wantSent: "POST " + projPath + "/sandboxes/try-upgrade/stop", stderr: []string{"try-upgrade: stopped"}},
		{name: "sandboxes start", args: []string{"sandboxes", "start", "try-upgrade"},
			routes:   [][3]string{{"POST " + projPath + "/sandboxes/try-upgrade/start", "200", "createSandbox.json"}},
			wantSent: "POST " + projPath + "/sandboxes/try-upgrade/start"},
		{name: "sandboxes create: bad runtime", args: []string{"sandboxes", "create", "x", "--runtime", "ruby"}, wantErr: "--runtime must be one of"},
		{name: "alerts enable", args: []string{"alerts", "enable", "alr_test1"},
			routes:   [][3]string{{"PATCH " + projPath + "/alerts/alr_test1", "200", "createAlert.json"}},
			wantSent: "PATCH " + projPath + "/alerts/alr_test1", wantBody: map[string]any{"on": true},
			stderr: []string{"Errors is on"}},
		{name: "alerts events", args: []string{"alerts", "events", "alr_test1"},
			routes: [][3]string{{"GET " + projPath + "/alerts/alr_test1/events", "200", "listAlertEvents.json"}},
			stdout: []string{"fired", "7.5"}},
		{name: "alerts rm", args: []string{"alerts", "rm", "alr_test1", "--yes"},
			routes:   [][3]string{{"DELETE " + projPath + "/alerts/alr_test1", "204", ""}},
			wantSent: "DELETE " + projPath + "/alerts/alr_test1"},
		{name: "alerts create: bad window", args: []string{"alerts", "create", "x", "--metric", "p99", "--threshold", "1", "--window", "7"}, wantErr: "--window must be"},
		{name: "usage json", args: []string{"usage", "--json", "--month", "2026-10"},
			routes:  [][3]string{{"GET " + teams + "/usage", "200", "getUsage.json"}},
			wantGet: "GET " + teams + "/usage", wantQuery: "month=2026-10",
			stdout: []string{`"logs_available": true`}},
		{name: "activity --all", args: []string{"activity", "--all", "--since", "48h", "-q"},
			routes: [][3]string{{"GET " + teams + "/activity", "200", "listActivity.json"}},
			stdout: []string{"act_test1"}},
		{name: "activity: bad since", args: []string{"activity", "--since", "yesterday"}, wantErr: "--since is a duration"},
		{name: "--team overrides the link's", args: []string{"--team", "other", "domains", "ls", "--all"},
			routes: [][3]string{{"GET /teams/other/domains", "200", "listDomains.json"}},
			stdout: []string{"shop.example.com"}},
	})
}

func TestCompletion(t *testing.T) {
	r := runWith(t, nil, opts{noToken: true}, "completion", "zsh")
	if r.err != nil || !strings.Contains(r.stderr+r.stdout, "orbits") {
		t.Fatalf("err %v", r.err)
	}
}
