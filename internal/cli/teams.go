package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/The-Orbits/orbits-cli/internal/config"
)

func newTeamsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teams",
		Short: "List the teams you are signed in to, and switch between them",
		Long: `A token acts inside one team, so each team you work in has its own profile:
sign in to another with "orbits --profile <name> login", choosing the team in
the browser, then switch between them with "orbits teams switch".`,
	}
	cmd.AddCommand(newTeamsListCmd(a), newTeamsSwitchCmd(a))
	return cmd
}

type teamRow struct {
	Profile string `json:"profile,omitempty"`
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	Access  string `json:"access,omitempty"`
	Current bool   `json:"current"`
}

func newTeamsListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your teams: one per signed-in profile",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var rows []teamRow
			envToken := os.Getenv(EnvToken) != ""
			if !envToken {
				for _, name := range a.cfg.Names() {
					p := a.cfg.Profiles[name]
					rows = append(rows, teamRow{Profile: name, ID: p.TeamID, Slug: p.TeamSlug, Name: p.TeamName, Access: p.Access, Current: name == a.profileName()})
				}
			}
			// The token in effect says its team, and the person's role in it.
			if c, err := a.client(); err == nil {
				teams, err := collect(c.Teams.All(ctx(cmd), nil), 0)
				if err != nil {
					return err
				}
				for _, t := range teams {
					found := false
					for i := range rows {
						if rows[i].ID == t.ID && rows[i].Current {
							rows[i].Role, rows[i].Name, rows[i].Slug = string(t.Role), t.Name, t.Slug
							found = true
						}
					}
					if !found {
						rows = append(rows, teamRow{ID: t.ID, Slug: t.Slug, Name: t.Name, Role: string(t.Role), Current: envToken})
					}
				}
			} else if !errors.Is(err, errNotSignedIn) || len(rows) == 0 {
				return err
			}
			if printList(a, rows, func(r teamRow) string { return r.Slug }, "No teams yet.", "Sign in", "orbits login") {
				return nil
			}
			table := make([][]string, len(rows))
			for i, r := range rows {
				mark := " "
				if r.Current {
					mark = "*"
				}
				profile := r.Profile
				if profile == "" {
					profile = EnvToken
				}
				table[i] = []string{mark, r.Name, r.Slug, or(r.Role), or(r.Access), profile}
			}
			a.out.Table([]string{"", "team", "slug", "role", "access", "profile"}, table)
			if !envToken {
				a.out.Summary(count(len(rows), "team"), "switch with: orbits teams switch <team>")
			}
			return nil
		},
	}
}

func newTeamsSwitchCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "switch <team>",
		Short: "Make the profile signed in to a team the current one",
		Long: `Make the profile signed in to a team (by slug, team_ id or profile name)
the one commands use when --profile is not given.`,
		Example: `  orbits teams switch acme`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			want := args[0]
			var match []string
			for _, name := range a.cfg.Names() {
				p := a.cfg.Profiles[name]
				if p.TeamSlug == want || p.TeamID == want || name == want {
					match = append(match, name)
				}
			}
			switch len(match) {
			case 0:
				return fmt.Errorf("no profile is signed in to team %q\nSign in to it, choosing it in the browser: orbits --profile %s login", want, want)
			case 1:
			default:
				// Prefer a profile named after the team.
				for _, m := range match {
					if m == want {
						match = []string{m}
					}
				}
				if len(match) > 1 {
					return fmt.Errorf("profiles %v are all signed in to %q: use --profile", match, want)
				}
			}
			name := match[0]
			if _, err := config.LoadToken(name); err != nil {
				return err
			}
			a.cfg.Current = name
			if err := a.cfg.Save(); err != nil {
				return err
			}
			p := a.cfg.Profiles[name]
			a.out.Success("Now using %s (profile %q).", a.out.Bold(or(p.TeamName)), name)
			if os.Getenv(EnvToken) != "" {
				a.out.Warn("ORBITS_TOKEN is set, and it is used instead of any profile.")
			}
			return nil
		},
	}
}
