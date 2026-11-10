package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func printer(human, color bool) (*Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &Printer{Out: &out, Err: &errOut, human: human, color: color, errColor: color}, &out, &errOut
}

func TestTableAlignsColouredCells(t *testing.T) {
	p, out, _ := printer(true, true)
	p.Table([]string{"name", "state", "id"}, [][]string{
		{"web", p.State("ready"), p.Dim("prj_1")},
		{"storefront-staging", p.State("failed"), p.Dim("prj_2")},
	})
	lines := strings.Split(strings.TrimRight(ansi.ReplaceAllString(out.String(), ""), "\n"), "\n")
	column := func(line, sub string) int { return utf8.RuneCountInString(line[:strings.Index(line, sub)]) }
	col := column(lines[0], "ID")
	for _, l := range lines[1:] {
		if column(l, "prj_") != col {
			t.Fatalf("columns do not line up:\n%s", strings.Join(lines, "\n"))
		}
	}
}

func TestPipedOutputIsPlain(t *testing.T) {
	p, out, _ := printer(false, false)
	p.Table([]string{"state"}, [][]string{{p.State("ready")}})
	if got := out.String(); got != "STATE\nready\n" {
		t.Fatalf("piped table = %q: no symbols, no colour", got)
	}
}

func TestStateSymbols(t *testing.T) {
	p, _, _ := printer(true, false)
	for state, want := range map[string]string{
		"ready": "● ready", "valid": "● valid", "building": "◐ building", "queued": "◐ queued",
		"failed": "✗ failed", "misconfigured": "✗ misconfigured", "canceled": "○ canceled", "paused": "○ paused",
	} {
		if got := p.State(state); got != want {
			t.Errorf("State(%s) = %q, want %q", state, got, want)
		}
	}
}

func TestDetailLinesUpLabels(t *testing.T) {
	p, out, _ := printer(true, false)
	p.Print(Detail{Title: "web", State: "● ready", Subtitle: "prj_1", Sections: [][]Pair{
		{{"Workload", "static"}},
		{{"Production", "https://web.theorbits.app"}},
	}})
	want := "web  ● ready\nprj_1\n\n  Workload    static\n\n  Production  https://web.theorbits.app\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestErrorFormatting(t *testing.T) {
	p, _, errOut := printer(false, false)
	p.Error(errors.New("no project named \"x\"\nSee: orbits projects list"))
	if got := errOut.String(); got != "✗ No project named \"x\"\n  See: orbits projects list\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTimes(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	for in, want := range map[string]string{
		"2026-10-04T11:59:30Z": "just now",
		"2026-10-04T11:55:00Z": "5 min ago",
		"2026-10-03T12:00:00Z": "1 day ago",
		"2026-09-12T12:00:00Z": "22 days ago",
		"2027-01-02T12:00:00Z": "in 3 months",
	} {
		ts, _ := time.Parse(time.RFC3339, in)
		if got := Ago(ts); got != want {
			t.Errorf("Ago(%s) = %q, want %q", in, got, want)
		}
	}
	if AgoPtr(nil) != "—" || Ago(time.Time{}) != "—" || WhenPtr(nil) != "—" {
		t.Error("nil and zero should be a dash")
	}
	if WhenString("never-ish") != "never-ish" || WhenString("") != "—" {
		t.Error("unparseable times come back as they were")
	}
}

func TestElapsedSecondsBytes(t *testing.T) {
	for d, want := range map[time.Duration]string{4 * time.Second: "4s", 72 * time.Second: "1:12", 600 * time.Second: "10:00"} {
		if got := Elapsed(d); got != want {
			t.Errorf("Elapsed(%s) = %q, want %q", d, got, want)
		}
	}
	n := 42
	if Seconds(&n) != "42s" || Seconds(nil) != "—" {
		t.Error("Seconds")
	}
	for n, want := range map[int64]string{0: "0 B", 512: "512 B", 2048: "2 KB", 1536: "1.5 KB", 48 << 20: "48 MB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCleanStripsControlCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"web-1":                      "web-1",
		"a\x1b[2Jb":                  "a[2Jb",
		"x\x1b]8;;https://evil\x07y": "x]8;;https://evily",
		"tab\tnew\nline\rcr\x7f":     "tabnewlinecr",
		"c1\u009b31m\u0085":          "c131m",
		"bad\xffbyte":                "bad�byte",
		"héllo · Lagos":              "héllo · Lagos",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMessagesCleanTheirArguments(t *testing.T) {
	p, _, errOut := printer(true, false)
	p.Success("Created %s.", "x\x1b[2J")
	p.Note("Saved to %s", "y\x07")
	p.Next("Then", "orbits \x1b[2J")
	p.Error(errors.New("bad\x1b]0;t\x07 name\nSee: z\x1b[1A"))
	if got := errOut.String(); strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("stderr %q", got)
	}
}

func TestBuildPiped(t *testing.T) {
	p, out, errOut := printer(false, false)
	b := p.Build(true)
	two := 2
	b.Steps([]Step{{Key: "queued", Label: "Queued", State: "done", Seconds: &two}, {Key: "building", Label: "Building", State: "running"}})
	b.Line("npm run build\x1b[2J", "plain")
	b.Steps([]Step{{Key: "queued", Label: "Queued", State: "done", Seconds: &two}, {Key: "building", Label: "Building", State: "failed", Seconds: &two}})
	b.Close()
	got := errOut.String()
	for _, want := range []string{"✓ Queued", "… Building", "    npm run build[2J\n", "✗ Building"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr %q lacks %q", got, want)
		}
	}
	if strings.Count(got, "Queued") != 1 {
		t.Fatalf("a step that ended is printed once: %q", got)
	}
	if out.Len() != 0 {
		t.Fatalf("the build view wrote to stdout: %q", out.String())
	}
}

func TestBuildLiveSpinsAndStops(t *testing.T) {
	p, _, errOut := printer(true, false)
	p.live = true
	b := p.Build(false)
	b.Steps([]Step{{Key: "building", Label: "Building", State: "running"}})
	time.Sleep(250 * time.Millisecond)
	b.Line("not shown", "plain")
	b.Close()
	got := errOut.String()
	if !strings.Contains(got, "Building") || strings.Contains(got, "not shown") {
		t.Fatalf("stderr %q", got)
	}
}

func TestBuildJSONIsSilent(t *testing.T) {
	p, _, errOut := printer(false, false)
	p.JSON = true
	b := p.Build(true)
	b.Line("x", "plain")
	b.Steps([]Step{{Key: "building", Label: "Building", State: "done"}})
	b.Close()
	if errOut.Len() != 0 {
		t.Fatalf("--json printed %q", errOut.String())
	}
}
