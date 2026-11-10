// Package cli is the orbits command tree.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/api"
	"github.com/The-Orbits/orbits-cli/internal/auth"
	"github.com/The-Orbits/orbits-cli/internal/config"
	"github.com/The-Orbits/orbits-cli/internal/link"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

// Version is set at build time: -ldflags "-X …/internal/cli.Version=v0.1.0".
var Version = "dev"

// Environment variables the CLI reads.
const (
	EnvToken   = "ORBITS_TOKEN"   // a token to use instead of the stored sign-in (CI)
	EnvAPIURL  = "ORBITS_API_URL" // the API, with /v1 (staging)
	EnvWebURL  = "ORBITS_WEB_URL" // the web app, for sign-in and open (staging)
	EnvProfile = "ORBITS_PROFILE"
	EnvTeam    = "ORBITS_TEAM"
)

// pollInterval is how often a wait asks the API; tests shorten it.
var pollInterval = 2 * time.Second

type app struct {
	profile string
	team    string
	project string
	jsonOut bool
	quiet   bool
	yes     bool

	cfg *config.Config
	out *output.Printer

	// stdin is where questions are answered and tokens read; interactive
	// says whether a person is there to answer. Tests replace both.
	stdin       io.Reader
	in          *bufio.Reader
	interactive func() bool
	// maxRetries, when set, replaces the SDK's retry count (tests).
	maxRetries *int

	c       *orbits.Client
	linked  *link.Found
	linkErr error
	meTeam  string

	newerVersion <-chan string
}

// NewRoot builds the command tree. Call notice once the command has
// finished, and printed any error, to say if a newer version is out.
func NewRoot() (root *cobra.Command, notice func()) {
	a := &app{}
	return newRoot(a), func() { a.updateNotice(a.newerVersion) }
}

func newRoot(a *app) *cobra.Command {
	if a.stdin == nil {
		a.stdin = os.Stdin
	}
	if a.interactive == nil {
		a.interactive = output.Interactive
	}
	root := &cobra.Command{
		Use:   "orbits",
		Short: "Deploy and manage your Orbits projects from the terminal",
		Long: `Deploy and manage your Orbits projects from the terminal: projects,
deployments and their logs, environment variables, domains and DNS, feature
flags, workflows, sandboxes, alerts, usage and activity.

Start with "orbits login", then "orbits link" in a project's folder and
"orbits deploy". In CI, set ORBITS_TOKEN instead of signing in.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if a.cfg == nil {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				a.cfg = cfg
			}
			if a.out == nil {
				a.out = output.New(a.jsonOut, a.quiet)
			}
			a.out.JSON, a.out.Quiet = a.jsonOut, a.quiet
			a.newerVersion = a.startUpdateCheck(cmd)
			return nil
		},
	}
	root.SetVersionTemplate("orbits {{.Version}}\n")

	f := root.PersistentFlags()
	f.StringVarP(&a.profile, "profile", "p", os.Getenv(EnvProfile), "profile to use (default: the current one)")
	f.StringVar(&a.team, "team", os.Getenv(EnvTeam), "team slug or team_ id (default: the linked project's, else the profile's)")
	f.StringVar(&a.project, "project", "", "project slug or prj_ id (default: the folder's linked project)")
	f.BoolVar(&a.jsonOut, "json", false, "print JSON instead of a table")
	f.BoolVarP(&a.quiet, "quiet", "q", false, "print only ids")
	f.BoolVarP(&a.yes, "yes", "y", false, "do not ask before changes that delete something or change production")

	root.AddGroup(
		&cobra.Group{ID: "start", Title: "Getting started:"},
		&cobra.Group{ID: "deploy", Title: "Projects and deployments:"},
		&cobra.Group{ID: "config", Title: "Configuration:"},
		&cobra.Group{ID: "more", Title: "Also:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("start", newLoginCmd(a), newLogoutCmd(a), newWhoamiCmd(a), newLinkCmd(a), newUnlinkCmd(a))
	add("deploy", newDeployCmd(a), newProjectsCmd(a), newDeploymentsCmd(a), newLogsCmd(a), newOpenCmd(a))
	add("config", newEnvCmd(a), newDomainsCmd(a), newFlagsCmd(a), newTeamsCmd(a))
	add("more", newWorkflowsCmd(a), newSandboxesCmd(a), newAlertsCmd(a), newUsageCmd(a), newActivityCmd(a),
		newMCPCmd(a), newUpdateCmd(a), newVersionCmd(a))
	root.SetHelpCommandGroupID("more")
	root.SetCompletionCommandGroupID("more")

	markUsageErrors(root)
	explainErrors(a, root)
	return root
}

type usageError struct {
	err error
	cmd *cobra.Command
}

func (e *usageError) Error() string {
	return fmt.Sprintf("%v\nSee: %s --help", e.err, e.cmd.CommandPath())
}
func (e *usageError) Unwrap() error { return e.err }

// IsUsage reports whether err is the command used wrongly, so main can exit 2.
func IsUsage(err error) bool {
	var u *usageError
	return errors.As(err, &u) || strings.HasPrefix(err.Error(), "unknown command") || strings.HasPrefix(err.Error(), "unknown flag")
}

// IsNotSignedIn reports whether err is a missing sign-in, so main can exit 3.
func IsNotSignedIn(err error) bool { return errors.Is(err, errNotSignedIn) }

// IsBuildFailed reports whether err is a deployment or operation that
// finished but failed, so main can exit 5.
func IsBuildFailed(err error) bool {
	var d *orbits.DeploymentError
	var o *orbits.OperationError
	var f *buildFailed
	return errors.As(err, &d) || errors.As(err, &o) || errors.As(err, &f)
}

func markUsageErrors(cmd *cobra.Command) {
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return &usageError{err, c} })
	if check := cmd.Args; check != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := check(c, args); err != nil {
				return &usageError{err, c}
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands() {
		markUsageErrors(sub)
	}
}

// explainErrors runs every command's error through api.Explain, so the
// API's problems say what to do next.
func explainErrors(a *app, cmd *cobra.Command) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return a.explain(run(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		explainErrors(a, sub)
	}
}

func (a *app) explain(err error) error {
	if err == nil {
		return nil
	}
	team := a.meTeam
	if a.cfg != nil && team == "" && os.Getenv(EnvToken) == "" {
		team = a.cfg.Profiles[a.profileName()].Team()
	}
	return api.Explain(err, api.Context{Team: team, Login: a.loginCommand(), WebURL: a.webURL(), BaseURL: a.baseURL()})
}

func (a *app) profileName() string { return a.cfg.Name(a.profile) }

func (a *app) currentProfile() config.Profile { return a.cfg.Profiles[a.profileName()] }

// baseURL is the API in effect: ORBITS_API_URL, else the one the profile
// signed in on, else production.
func (a *app) baseURL() string {
	if u := strings.TrimSpace(os.Getenv(EnvAPIURL)); u != "" {
		return strings.TrimRight(u, "/")
	}
	if a.cfg != nil && os.Getenv(EnvToken) == "" {
		if u := a.currentProfile().APIURL; u != "" {
			return u
		}
	}
	return api.DefaultBaseURL
}

// webURL is the web app: ORBITS_WEB_URL, else https://orbits.africa.
func (a *app) webURL() string {
	if u := strings.TrimSpace(os.Getenv(EnvWebURL)); u != "" {
		return strings.TrimRight(u, "/")
	}
	return auth.DefaultWebURL
}

var errNotSignedIn = errors.New(`not signed in: run "orbits login", or set ORBITS_TOKEN`)

// token is the token in effect and where it came from.
func (a *app) token() (string, string, error) {
	if t := strings.TrimSpace(os.Getenv(EnvToken)); t != "" {
		return t, EnvToken, nil
	}
	t, err := config.LoadToken(a.profileName())
	if err != nil {
		return "", "", fmt.Errorf("reading the stored token: %w", err)
	}
	if t == "" {
		if a.profile != "" {
			return "", "", fmt.Errorf("%w (profile %q has no sign-in)", errNotSignedIn, a.profile)
		}
		return "", "", errNotSignedIn
	}
	return t, "profile " + a.profileName(), nil
}

// client is the API client with the token in effect.
func (a *app) client() (*orbits.Client, error) {
	if a.c != nil {
		return a.c, nil
	}
	t, _, err := a.token()
	if err != nil {
		return nil, err
	}
	c, err := a.newClient(t, a.baseURL())
	if err != nil {
		return nil, err
	}
	a.c = c
	return c, nil
}

func (a *app) newClient(token, baseURL string) (*orbits.Client, error) {
	return api.New(token, api.Options{BaseURL: baseURL, Version: Version, MaxRetries: a.maxRetries})
}

func (a *app) loginCommand() string {
	if a.profile == "" {
		return "orbits login"
	}
	return "orbits --profile " + a.profile + " login"
}

// confirm asks before something that deletes or changes production. --yes
// skips it; with no terminal to ask in, it refuses rather than guess.
func (a *app) confirm(question string) error {
	if a.yes {
		return nil
	}
	if !a.interactive() {
		return errors.New(question + " Pass --yes to go ahead without asking.")
	}
	fmt.Fprintf(a.out.Err, "%s [y/N] ", output.Clean(question))
	line, _ := a.readLine()
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errCancelled
}

// ask asks a question and returns the answer, trimmed.
func (a *app) ask(question string) (string, error) {
	if !a.interactive() {
		return "", errors.New("no terminal to ask in")
	}
	fmt.Fprintf(a.out.Err, "%s ", question)
	line, err := a.readLine()
	if err != nil && line == "" {
		return "", errCancelled
	}
	return strings.TrimSpace(line), nil
}

// readLine reads one line of stdin, through one buffer for the whole run.
func (a *app) readLine() (string, error) {
	if a.in == nil {
		a.in = bufio.NewReader(a.stdin)
	}
	return a.in.ReadString('\n')
}

var errCancelled = errors.New("cancelled")

func ctx(cmd *cobra.Command) context.Context { return cmd.Context() }
