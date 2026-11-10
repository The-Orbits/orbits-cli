package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/link"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

// findLink is the folder's link, looked for once.
func (a *app) findLink() (*link.Found, error) {
	if a.linked == nil && a.linkErr == nil {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		a.linked, a.linkErr = link.Find(wd)
	}
	return a.linked, a.linkErr
}

// teamRef is the team commands act in, by slug or id: --team (or
// ORBITS_TEAM), else the linked project's, else the profile's; with
// ORBITS_TOKEN and nothing else, the token's own team, from GET /me.
func (a *app) teamRef(cmd *cobra.Command) (string, error) {
	if a.team != "" {
		a.meTeam = a.team
		return a.team, nil
	}
	if l, err := a.findLink(); err == nil {
		a.meTeam = l.TeamSlug
		if a.meTeam == "" {
			a.meTeam = l.TeamID
		}
		return l.TeamID, nil
	} else if !errors.Is(err, link.ErrNotLinked) {
		return "", err
	}
	if os.Getenv(EnvToken) == "" {
		if t := a.currentProfile().Team(); t != "" {
			a.meTeam = t
			return t, nil
		}
	}
	c, err := a.client()
	if err != nil {
		return "", err
	}
	me, err := c.Me.Get(ctx(cmd))
	if err != nil {
		return "", err
	}
	switch len(me.Teams) {
	case 0:
		return "", errors.New("this token reaches no team: make one under Team › Settings › API tokens")
	case 1:
		a.meTeam = me.Teams[0].Slug
		return me.Teams[0].Slug, nil
	}
	return "", &usageError{errors.New("this token reaches more than one team: pass --team <slug>"), cmd}
}

// errNoProject is a project-level command run outside a linked folder
// without --project.
var errNoProject = errors.New("no project: run this in a folder linked with \"orbits link\", or pass --project <slug>")

// projectRef is the project (and its team) a command acts on: the argument
// given, else --project, else the folder's link. A project from the link
// takes the link's team unless --team says otherwise.
func (a *app) projectRef(cmd *cobra.Command, arg string) (team, project string, err error) {
	ref := arg
	if ref == "" {
		ref = a.project
	}
	if ref != "" {
		team, err = a.teamRef(cmd)
		return team, ref, err
	}
	l, err := a.findLink()
	if errors.Is(err, link.ErrNotLinked) {
		return "", "", errNoProject
	}
	if err != nil {
		return "", "", err
	}
	team = l.TeamID
	if a.team != "" {
		team = a.team
	}
	a.meTeam = l.TeamSlug
	if a.meTeam == "" {
		a.meTeam = l.TeamID
	}
	return team, l.ProjectID, nil
}

// clientAndProject is the client with the project a command acts on.
func (a *app) clientAndProject(cmd *cobra.Command, arg string) (*orbits.Client, string, string, error) {
	c, err := a.client()
	if err != nil {
		return nil, "", "", err
	}
	team, project, err := a.projectRef(cmd, arg)
	if err != nil {
		return nil, "", "", err
	}
	return c, team, project, nil
}

// clientAndTeam is the client with the team a command acts in.
func (a *app) clientAndTeam(cmd *cobra.Command) (*orbits.Client, string, error) {
	c, err := a.client()
	if err != nil {
		return nil, "", err
	}
	team, err := a.teamRef(cmd)
	if err != nil {
		return nil, "", err
	}
	return c, team, nil
}

// printList prints items as JSON or ids when asked, or the empty note; it
// returns false when the caller should print its table.
func printList[T any](a *app, items []T, id func(T) string, empty string, next ...string) bool {
	switch {
	case a.out.JSON:
		if items == nil {
			items = []T{}
		}
		a.out.Value(items)
	case a.out.Quiet:
		for _, it := range items {
			a.out.Line("%s", id(it))
		}
	case len(items) == 0:
		a.out.Note("%s", empty)
		if len(next) == 2 {
			a.out.Next(next[0], next[1])
		}
	default:
		return false
	}
	return true
}

// printValue prints v as JSON (--json) or its id (--quiet), reporting
// whether it did; otherwise the caller prints it for a person.
func printValue(a *app, v any, id string) bool {
	switch {
	case a.out.JSON:
		a.out.Value(v)
	case a.out.Quiet:
		if id != "" {
			a.out.Line("%s", id)
		}
	default:
		return false
	}
	return true
}

// collect drains an iterator from orbits-go, up to limit items (0: all).
func collect[T any](seq func(func(T, error) bool), limit int) ([]T, error) {
	var out []T
	var err error
	seq(func(v T, e error) bool {
		if e != nil {
			err = e
			return false
		}
		out = append(out, v)
		return limit <= 0 || len(out) < limit
	})
	return out, err
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func or(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// short is the first 7 characters of a commit sha.
func short(sha *string) string {
	if sha == nil || *sha == "" {
		return "—"
	}
	if len(*sha) > 7 {
		return (*sha)[:7]
	}
	return *sha
}

// splitList reads "a,b, c" as [a b c].
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func oneOf[T ~string](flag, v string, allowed ...T) (T, error) {
	for _, x := range allowed {
		if string(x) == v {
			return x, nil
		}
	}
	names := make([]string, len(allowed))
	for i, x := range allowed {
		names[i] = string(x)
	}
	return "", fmt.Errorf("--%s must be one of %s, not %q", flag, strings.Join(names, ", "), v)
}

func state(out *output.Printer, s string) string { return out.State(s) }
