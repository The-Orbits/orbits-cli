// Package link ties a folder to an Orbits project: .orbits/project.json,
// which commands run in that folder (or below it) read for their team and
// project. The folder is kept out of Git.
package link

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Dir is the folder the link lives in, at the project's root.
const Dir = ".orbits"

// File is the link inside Dir.
const File = "project.json"

// Link is .orbits/project.json. Ids are what commands use, so renaming the
// team or the project does not break it; slugs are for people reading it.
type Link struct {
	TeamID      string `json:"team_id"`
	TeamSlug    string `json:"team_slug,omitempty"`
	ProjectID   string `json:"project_id"`
	ProjectSlug string `json:"project_slug,omitempty"`
}

// Found is a link and the folder it is in.
type Found struct {
	Link
	Root string // the folder holding .orbits
}

// ErrNotLinked is a folder with no link in it or above it.
var ErrNotLinked = errors.New("this folder is not linked to a project")

// Find looks for .orbits/project.json in dir and each folder above it,
// stopping at the first, or at the root of a Git repository.
func Find(dir string) (*Found, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for d := abs; ; {
		p := filepath.Join(d, Dir, File)
		data, err := os.ReadFile(p)
		switch {
		case err == nil:
			var l Link
			if err := json.Unmarshal(data, &l); err != nil {
				return nil, fmt.Errorf("%s is not valid JSON: %w\nLink the folder again: orbits link", p, err)
			}
			if l.ProjectID == "" || l.TeamID == "" {
				return nil, fmt.Errorf("%s has no project or team in it\nLink the folder again: orbits link", p)
			}
			return &Found{Link: l, Root: d}, nil
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return nil, ErrNotLinked
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil, ErrNotLinked
		}
		d = parent
	}
}

// Write saves the link in root/.orbits/project.json (the folder 0755, the
// file 0644: it holds ids, no secrets), refusing to write through a link.
func Write(root string, l Link) (string, error) {
	dir := filepath.Join(root, Dir)
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%s exists and is not a folder", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, File)
	if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file: refusing to write it", p)
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	readme := filepath.Join(dir, "README.txt")
	if _, err := os.Lstat(readme); errors.Is(err, fs.ErrNotExist) {
		_ = os.WriteFile(readme, []byte(readmeText), 0o644)
	}
	return p, atomicWrite(p, data, 0o644)
}

const readmeText = `This folder links the project to Orbits (https://orbits.africa).

project.json names the team and project that "orbits" commands run in this
folder use. It holds no secrets, but it is yours, not your teammates': keep
the folder out of Git. "orbits link" made it; delete the folder to unlink.
`

// Remove deletes the link (the whole .orbits folder) under root.
func Remove(root string) error {
	return os.RemoveAll(filepath.Join(root, Dir))
}

// IgnoreGit adds ".orbits" to root's .gitignore, creating it, unless a line
// already ignores it. It reports whether it changed the file.
func IgnoreGit(root string) (bool, error) {
	p := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case ".orbits", ".orbits/", "/.orbits", "/.orbits/":
			return false, nil
		}
	}
	var b bytes.Buffer
	b.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString(".orbits\n")
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		mode = info.Mode().Perm()
	}
	return true, atomicWrite(p, b.Bytes(), mode)
}

func atomicWrite(p string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
