package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/api"
	"github.com/The-Orbits/orbits-cli/internal/auth"
	"github.com/The-Orbits/orbits-cli/internal/config"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

// openBrowser opens a URL in the browser; tests replace it.
var openBrowser = auth.OpenBrowser

func newLoginCmd(a *app) *cobra.Command {
	var withToken, noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in through the web app in your browser",
		Long: `Sign in through the Orbits web app in your browser. You choose the team, the
access (full, or read only) and how long it lasts there, and the CLI receives
an API token for that team, which is stored in your system's keychain. The
team becomes this profile's default.

A token acts inside one team. To work in several, sign in once per team with
a profile each: orbits --profile <name> login, then orbits teams switch.

Without a browser (a server, CI), make a token under Team › Settings › API
tokens and pass it on stdin:

  orbits login --with-token < token.txt

or store nothing and set ORBITS_TOKEN.`,
		Example: `  orbits login
  orbits --profile acme login
  echo "$ORBITS_TOKEN" | orbits login --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name := a.profileName()
			profile := config.Profile{}
			var me *orbits.Me
			var apiURL string

			if withToken {
				token, err := a.readToken()
				if err != nil {
					return err
				}
				apiURL = a.loginAPI("")
				if me, err = a.whoAmI(ctx(cmd), apiURL, token); err != nil {
					return err
				}
				if len(me.Teams) == 0 {
					return errors.New("this token reaches no team: make one under Team › Settings › API tokens")
				}
				t := me.Teams[0]
				for _, mt := range me.Teams {
					if me.Token != nil && mt.ID == me.Token.TeamID {
						t = mt
					}
				}
				profile.TeamID, profile.TeamSlug, profile.TeamName = t.ID, t.Slug, t.Name
				// The API says what a pasted token may do, so the profile knows as much as after a browser sign-in.
				if me.Token != nil {
					profile.TokenID, profile.Access = me.Token.ID, string(me.Token.Access)
				}
				if err := a.saveSignIn(name, token, profile, me, apiURL); err != nil {
					return err
				}
				return nil
			}

			host, _ := os.Hostname()
			host = strings.TrimSuffix(host, ".local")
			b := &auth.Browser{
				WebURL: a.webURL(),
				Host:   host,
				Prompt: func(url string, opened bool) {
					if opened {
						a.out.Note("Opened your browser to approve the CLI. If it did not open, go to:\n\n  %s\n", url)
					} else {
						a.out.Note("Open this in your browser to approve the CLI:\n\n  %s\n", url)
					}
					a.out.Note("Waiting for approval…")
				},
				// Checked before the browser tab is told it worked.
				Verify: func(c context.Context, g *auth.Grant) error {
					apiURL = a.loginAPI(g.APIBase())
					var err error
					if me, err = a.whoAmI(c, apiURL, g.Token); err != nil {
						return fmt.Errorf("%w\nThe web app made token %s for this sign-in: revoke it under Team › Settings › API tokens", err, g.TokenID)
					}
					return nil
				},
			}
			if !noBrowser {
				b.Open = openBrowser
			}
			grant, err := b.Login(ctx(cmd))
			if err != nil {
				return err
			}
			profile.TeamID, profile.TeamSlug, profile.TeamName = grant.Team.ID, grant.Team.Slug, grant.Team.Name
			profile.TokenID, profile.TokenName, profile.Access = grant.TokenID, grant.TokenName, grant.Access
			if me != nil && me.Token != nil && profile.Access == "" {
				profile.Access = string(me.Token.Access)
			}
			if grant.ExpiresAt != nil {
				profile.ExpiresAt = *grant.ExpiresAt
			}
			return a.saveSignIn(name, grant.Token, profile, me, apiURL)
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read an API token from stdin instead of signing in in a browser")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in link instead of opening a browser")
	return cmd
}

// loginAPI is the API a new sign-in is checked against and kept for:
// ORBITS_API_URL, else the one the web app named, else production.
func (a *app) loginAPI(granted string) string {
	if u := strings.TrimSpace(os.Getenv(EnvAPIURL)); u != "" {
		return strings.TrimRight(u, "/")
	}
	if granted != "" {
		return granted
	}
	return api.DefaultBaseURL
}

func (a *app) whoAmI(c context.Context, baseURL, token string) (*orbits.Me, error) {
	client, err := a.newClient(token, baseURL)
	if err != nil {
		return nil, err
	}
	me, err := client.Me.Get(c)
	if err != nil {
		return nil, fmt.Errorf("the token did not work against %s: %w", baseURL, a.explain(err))
	}
	return me, nil
}

func (a *app) saveSignIn(name, token string, profile config.Profile, me *orbits.Me, apiURL string) error {
	if apiURL != api.DefaultBaseURL {
		profile.APIURL = apiURL
	}
	if me != nil {
		profile.UserEmail = me.Email
	}
	where, err := config.SaveToken(name, token)
	if err != nil {
		return fmt.Errorf("storing the token: %w", err)
	}
	a.cfg.Profiles[name] = profile
	if a.cfg.Current == "" {
		a.cfg.Current = name
	}
	if err := a.cfg.Save(); err != nil {
		return err
	}
	team := profile.TeamName
	if team == "" {
		team = profile.Team()
	}
	access := ""
	switch profile.Access {
	case "read":
		access = ", read only"
	case "full":
		access = ", full access"
	}
	who := ""
	if me != nil && me.Email != "" {
		who = " as " + me.Email
	}
	a.out.Success("Signed in to %s%s%s, profile %q.", a.out.Bold(team), who, access, name)
	if where == config.InFile {
		p, _ := config.CredentialsPath()
		a.out.Note("No keychain here, so the token is in %s, readable only by you.", p)
	}
	if os.Getenv(EnvToken) != "" {
		a.out.Warn("ORBITS_TOKEN is set, and it is used instead of this sign-in until you unset it.")
	}
	a.out.Next("Next", "orbits link, in a project's folder")
	return nil
}

// readToken reads a token from stdin, hidden when typed at a terminal.
func (a *app) readToken() (string, error) {
	var token string
	if f, ok := a.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(a.out.Err, "Paste your API token: ")
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.out.Err)
		if err != nil {
			return "", err
		}
		token = string(raw)
	} else {
		line, err := a.readLine()
		if err != nil && line == "" {
			return "", errors.New("no token on stdin")
		}
		token = line
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "orb_") {
		return "", errors.New("that is not an Orbits API token: tokens start with orb_live_. Copy the whole token from Team › Settings › API tokens")
	}
	return token, nil
}

func newLogoutCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out: forget this profile's token on this machine",
		Long: `Sign out: forget this profile's token on this machine. The token keeps
working until it expires or is revoked under Team › Settings › API tokens
(API tokens can't revoke themselves).

ORBITS_TOKEN is never touched: unset it yourself.`,
		Example: `  orbits logout
  orbits --profile acme logout`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name := a.profileName()
			profile, known := a.cfg.Profiles[name]
			stored, _ := config.LoadToken(name)
			if err := config.DeleteToken(name); err != nil {
				return err
			}
			delete(a.cfg.Profiles, name)
			if a.cfg.Current == name {
				a.cfg.Current = ""
			}
			if err := a.cfg.Save(); err != nil {
				return err
			}
			if !known && stored == "" {
				a.out.Note("Profile %q was not signed in.", name)
				return nil
			}
			a.out.Success("Signed out of profile %q.", name)
			tok := "Its token"
			if profile.TokenName != "" {
				tok = fmt.Sprintf("Its token (%s)", profile.TokenName)
			}
			a.out.Note("%s still works until it expires. Revoke it under Team › Settings › API tokens to end it now.", tok)
			if os.Getenv(EnvToken) != "" {
				a.out.Warn("ORBITS_TOKEN is still set, so commands still sign in with it.")
			}
			return nil
		},
	}
}

type whoami struct {
	User    orbits.User  `json:"user"`
	Team    *orbits.Team `json:"team"`
	Profile string       `json:"profile,omitempty"`
	Source  string       `json:"source"`
	APIURL  string       `json:"api_url"`
	Token   *tokenInfo   `json:"token,omitempty"`
}

type tokenInfo struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Access    string `json:"access,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// ProjectID is the one project the token is limited to; empty for every project of its team.
	ProjectID string `json:"project_id,omitempty"`
	// Project is that project's slug, when the CLI could see it.
	Project string `json:"project,omitempty"`
}

// tokenFrom is what the API says about the token in effect, completed with what the profile remembers of it
// (its name and expiry, which only a browser sign-in is told). Nil for a session, which the CLI never has.
func tokenFrom(me *orbits.Me, p *config.Profile) *tokenInfo {
	t := &tokenInfo{}
	if p != nil {
		t.ID, t.Name, t.Access, t.ExpiresAt = p.TokenID, p.TokenName, p.Access, p.ExpiresAt
	}
	if mt := me.Token; mt != nil {
		t.ID, t.Access = mt.ID, string(mt.Access)
		if mt.ProjectID != nil {
			t.ProjectID = *mt.ProjectID
			for _, team := range me.Teams {
				for _, pr := range team.Projects {
					if pr.ID == t.ProjectID {
						t.Project = pr.Slug
					}
				}
			}
		}
	}
	if *t == (tokenInfo{}) {
		return nil
	}
	return t
}

func newWhoamiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who and which team the CLI acts as",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, source, err := a.token()
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			me, err := c.Me.Get(ctx(cmd))
			if err != nil {
				return err
			}
			w := whoami{User: me.User, Source: source, APIURL: c.BaseURL()}
			if len(me.Teams) > 0 {
				w.Team = &me.Teams[0].Team
			}
			var profile *config.Profile
			if source != EnvToken {
				p := a.currentProfile()
				profile = &p
				w.Profile = a.profileName()
			}
			w.Token = tokenFrom(me, profile)
			for _, t := range me.Teams {
				if (me.Token != nil && t.ID == me.Token.TeamID) || (me.Token == nil && profile != nil && t.ID == profile.TeamID) {
					w.Team = &t.Team
				}
			}
			if w.Team != nil {
				a.meTeam = w.Team.Slug
			}
			if printValue(a, w, me.ID) {
				return nil
			}
			team := "—"
			if w.Team != nil {
				team = w.Team.Name + "  " + a.out.Dim(w.Team.Slug+" · "+string(w.Team.Role))
			}
			sections := [][]output.Pair{{{"Team", team}}}
			if t := w.Token; t != nil {
				access := map[string]string{"full": "full", "read": "read only"}[t.Access]
				projects := "every project of the team"
				switch {
				case t.Project != "":
					projects = "only " + t.Project + "  " + a.out.Dim(t.ProjectID)
				case t.ProjectID != "":
					projects = "only " + t.ProjectID
				}
				pairs := []output.Pair{{"Token", or(t.Name) + "  " + a.out.Dim(t.ID)}, {"Access", or(access)}, {"Projects", projects}}
				// Only a browser sign-in is told when the token expires; for any other, it is unknown, not "never".
				if source != EnvToken && (t.Name != "" || t.ExpiresAt != "") {
					expires := "never"
					if t.ExpiresAt != "" {
						expires = output.WhenString(t.ExpiresAt)
					}
					pairs = append(pairs, output.Pair{"Expires", expires})
				}
				sections = append(sections, pairs)
			}
			sections = append(sections, []output.Pair{{"API", c.BaseURL()}, {"From", source}})
			a.out.Print(output.Detail{Title: or(me.Name), Subtitle: me.Email, Sections: sections})
			return nil
		},
	}
}
