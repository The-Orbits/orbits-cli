package link

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteThenFindFromBelow(t *testing.T) {
	root := t.TempDir()
	want := Link{TeamID: "team_1", TeamSlug: "acme", ProjectID: "prj_1", ProjectSlug: "web"}
	p, err := Write(root, want)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, ".orbits", "project.json") {
		t.Fatalf("path = %s", p)
	}
	sub := filepath.Join(root, "src", "pages")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(sub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Link != want {
		t.Fatalf("Find = %+v, want %+v", got.Link, want)
	}
	if real, _ := filepath.EvalSymlinks(root); got.Root != root && got.Root != real {
		t.Fatalf("Root = %s, want %s", got.Root, root)
	}
	if _, err := os.Stat(filepath.Join(root, ".orbits", "README.txt")); err != nil {
		t.Fatalf("no README: %v", err)
	}
}

func TestFindStopsAtTheGitRoot(t *testing.T) {
	outer := t.TempDir()
	if _, err := Write(outer, Link{TeamID: "team_1", ProjectID: "prj_outer"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(repo); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("a link outside the repository was used: %v", err)
	}
}

func TestFindRejectsABrokenLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".orbits"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"not json": "{", "empty": "{}"} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, ".orbits", "project.json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Find(root); err == nil || errors.Is(err, ErrNotLinked) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestWriteRefusesASymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".orbits"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(root, ".orbits", "project.json")); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := Write(root, Link{TeamID: "t", ProjectID: "p"}); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the link's target was created")
	}
}

func TestIgnoreGit(t *testing.T) {
	for _, c := range []struct {
		name, before, after string
		changed             bool
	}{
		{"no file", "", ".orbits\n", true},
		{"no newline at the end", "node_modules", "node_modules\n.orbits\n", true},
		{"already there", "dist\n.orbits/\n", "dist\n.orbits/\n", false},
		{"anchored", "/.orbits\n", "/.orbits\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.before != "" {
				if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(c.before), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := IgnoreGit(root)
			if err != nil || changed != c.changed {
				t.Fatalf("IgnoreGit = %v, %v", changed, err)
			}
			data, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
			if string(data) != c.after {
				t.Fatalf(".gitignore = %q, want %q", data, c.after)
			}
			if c.before != "" {
				info, _ := os.Stat(filepath.Join(root, ".gitignore"))
				if info.Mode().Perm() != 0o600 {
					t.Fatalf("mode changed to %v", info.Mode().Perm())
				}
			}
		})
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root, Link{TeamID: "t", ProjectID: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(root); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("still linked: %v", err)
	}
}
