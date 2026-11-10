package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

var allEnvironments = []orbits.EnvironmentName{orbits.EnvironmentNameProduction, orbits.EnvironmentNamePreview, orbits.EnvironmentNameDevelopment}

func parseEnvironments(s string) ([]orbits.EnvironmentName, error) {
	var out []orbits.EnvironmentName
	for _, part := range splitList(s) {
		e, err := oneOf("environment", part, allEnvironments...)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func newEnvCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage the project's environment variables",
		Long: `Manage the project's environment variables. Values are never printed unless
you ask (ls --show-values, pull): a sensitive variable is write-only, and
nobody can read it back. Reading values needs a full-access token.

A changed variable takes effect on the next deployment.`,
	}
	cmd.AddCommand(newEnvListCmd(a), newEnvAddCmd(a), newEnvRemoveCmd(a), newEnvPullCmd(a), newEnvRestoreCmd(a))
	return cmd
}

func newEnvListCmd(a *app) *cobra.Command {
	var environment string
	var showValues bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the variables: keys, environments, sensitive or not",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			params := &orbits.ListEnvVarsParams{Limit: 100}
			if environment != "" {
				e, err := oneOf("environment", environment, allEnvironments...)
				if err != nil {
					return &usageError{err, cmd}
				}
				params.Environment = e
			}
			vars, err := collect(c.Env.All(ctx(cmd), team, project, params), 0)
			if err != nil {
				return err
			}
			for i := range vars {
				v := &vars[i]
				switch {
				case !showValues:
					v.Value = nil
				case v.Value == nil && !v.Sensitive:
					got, err := c.Env.Reveal(ctx(cmd), team, project, v.Key)
					if err != nil {
						return err
					}
					v.Value = &got.Value
				}
			}
			if printList(a, vars, func(v orbits.EnvVar) string { return v.Key }, "No environment variables yet.", "Add one", "orbits env add <KEY>") {
				return nil
			}
			rows := make([][]string, len(vars))
			for i, v := range vars {
				envs := make([]string, len(v.Environments))
				for j, e := range v.Environments {
					envs[j] = string(e)
				}
				kind := "plain"
				if v.Sensitive {
					kind = "sensitive"
				}
				row := []string{v.Key, strings.Join(envs, ", "), kind, output.Ago(v.UpdatedAt)}
				if showValues {
					val := a.out.Dim("(write-only)")
					if v.Value != nil {
						val = output.Clean(*v.Value)
					}
					row = append(row, val)
				}
				if v.PendingRedeploy {
					row[0] += " " + a.out.Dim("(new)")
				}
				rows[i] = row
			}
			header := []string{"key", "environments", "kind", "updated"}
			if showValues {
				header = append(header, "value")
			}
			a.out.Table(header, rows)
			a.out.Summary(count(len(vars), "variable"))
			return nil
		},
	}
	cmd.Flags().StringVar(&environment, "environment", "", "only variables used by production, preview or development")
	cmd.Flags().BoolVar(&showValues, "show-values", false, "print values too (sensitive ones are write-only)")
	return cmd
}

func newEnvAddCmd(a *app) *cobra.Command {
	var environments string
	var plain, force bool
	cmd := &cobra.Command{
		Use:     "add <KEY> [value]",
		Aliases: []string{"set"},
		Short:   "Add a variable (or replace one, with --force)",
		Long: `Add a variable. Without a value on the command line it is read from stdin:
typed hidden at a terminal, or piped, which keeps it out of your shell's
history.

Variables are sensitive (write-only) unless --plain: a sensitive value can be
replaced but never read back, not even by orbits env pull.`,
		Example: `  orbits env add DATABASE_URL
  printf %s "$STRIPE_KEY" | orbits env add STRIPE_KEY --environment production
  orbits env add NEXT_PUBLIC_SITE_NAME Storefront --plain
  orbits env add API_URL https://api.example.com --force`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			envs, err := parseEnvironments(environments)
			if err != nil {
				return &usageError{err, cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			var value string
			if len(args) == 2 {
				value = args[1]
			} else if value, err = a.readValue(key); err != nil {
				return err
			}
			sensitive := !plain
			input := orbits.EnvVarInput{Key: key, Value: value, Environments: envs, Sensitive: &sensitive}
			req := orbits.EnvVarCreate{Variables: []orbits.EnvVarInput{input}}
			if force {
				req.OnConflict = orbits.Ptr(orbits.EnvVarCreateOnConflictReplace)
			}
			res, err := c.Env.Create(ctx(cmd), team, project, req)
			if orbits.IsConflict(err) && !force {
				return fmt.Errorf("%s exists already: replace it with --force\n%w", key, a.explain(err))
			}
			if err != nil {
				return err
			}
			if a.out.JSON {
				for i := range res.Items {
					res.Items[i].Value = nil
				}
				a.out.Value(res)
				return nil
			}
			verb := "Added"
			if res.Replaced > 0 {
				verb = "Replaced"
			}
			a.out.Success("%s %s.", verb, key)
			if res.RedeployRequired {
				a.out.Next("It takes effect on the next deployment", "orbits deployments redeploy")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&environments, "environment", "", "comma-separated: production, preview, development (default: production, preview)")
	f.BoolVar(&plain, "plain", false, "not sensitive: the value can be read back (and pulled)")
	f.BoolVar(&force, "force", false, "replace the variable if it exists")
	return cmd
}

// readValue reads a variable's value: hidden at a terminal, else all of
// stdin with one trailing newline removed.
func (a *app) readValue(key string) (string, error) {
	if f, ok := a.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprintf(a.out.Err, "Value of %s: ", key)
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.out.Err)
		return string(raw), err
	}
	data, err := io.ReadAll(io.LimitReader(a.stdin, 64<<10))
	if err != nil {
		return "", err
	}
	v := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	return v, nil
}

func newEnvRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <KEY>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a variable (restorable for a while)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Remove %s from %s?", args[0], project)); err != nil {
				return err
			}
			res, err := c.Env.Delete(ctx(cmd), team, project, args[0])
			if err != nil {
				return err
			}
			if printValue(a, res, res.Key) {
				return nil
			}
			a.out.Success("Removed %s.", res.Key)
			if !res.RestorableUntil.IsZero() {
				a.out.Next("Restore it until "+res.RestorableUntil.Local().Format("2 Jan 15:04"), "orbits env restore "+res.Key)
			}
			return nil
		},
	}
}

func newEnvRestoreCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <KEY>",
		Short: "Bring back a variable removed recently",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			res, err := c.Env.Restore(ctx(cmd), team, project, args[0])
			if err != nil {
				return err
			}
			res.Item.Value = nil
			if printValue(a, res, res.Item.Key) {
				return nil
			}
			a.out.Success("Restored %s.", res.Item.Key)
			return nil
		},
	}
}

// gitIgnored reports whether git ignores path: yes, no, or unknown (no git,
// not a repository). A variable so tests can stand in for git.
var gitIgnored = func(c context.Context, path string) (ignored, known bool) {
	git, err := exec.LookPath("git")
	if err != nil {
		return false, false
	}
	c, cancel := context.WithTimeout(c, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, git, "-C", filepath.Dir(path), "check-ignore", "-q", filepath.Base(path))
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, true
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, true
	}
	return false, false
}

func newEnvPullCmd(a *app) *cobra.Command {
	var environment string
	cmd := &cobra.Command{
		Use:   "pull [file]",
		Short: "Write the variables to a dotenv file (default .env.local)",
		Long: `Write an environment's variables (default: development) to a dotenv file,
readable only by you (mode 0600). Sensitive variables are write-only, so
they are left out and named. Replacing an existing file asks first.

It needs a full-access token: reading values is a change only full tokens
may make.`,
		Example: `  orbits env pull
  orbits env pull .env.production.local --environment production`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := ".env.local"
			if len(args) == 1 {
				file = args[0]
			}
			env, err := oneOf("environment", environment, allEnvironments...)
			if err != nil {
				return &usageError{err, cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := checkTarget(file, true); err != nil {
				return err
			}
			if _, err := os.Lstat(file); err == nil {
				if err := a.confirm(fmt.Sprintf("Replace %s?", file)); err != nil {
					return err
				}
			}
			pull, err := c.Env.Pull(ctx(cmd), team, project, env)
			if err != nil {
				return err
			}
			data := dotenv(pull, project)
			if err := writeFiles([]pendingFile{{path: file, data: data}}, 0o600); err != nil {
				return err
			}
			if a.out.JSON {
				a.out.Value(map[string]any{"file": file, "environment": pull.Environment, "keys": sortedKeys(pull.Variables), "skipped_sensitive": nonNil(pull.SkippedSensitive)})
				return nil
			}
			a.out.Success("Wrote %s to %s (%s).", count(len(pull.Variables), "variable"), file, pull.Environment)
			if n := len(pull.SkippedSensitive); n > 0 {
				a.out.Note("Left out %s: %s (sensitive, so write-only).", count(n, "sensitive variable"), strings.Join(pull.SkippedSensitive, ", "))
			}
			if ignored, known := gitIgnored(ctx(cmd), file); known && !ignored {
				a.out.Warn("%s is not ignored by Git: add it to .gitignore so it is never committed.", file)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&environment, "environment", "development", "production, preview or development")
	return cmd
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// dotenv writes variables as KEY="value" lines, sorted, escaped so dotenv
// readers give back the value exactly.
func dotenv(p *orbits.EnvVarPull, project string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by orbits env pull (%s, %s). Do not commit this file.\n", project, p.Environment)
	for _, k := range sortedKeys(p.Variables) {
		fmt.Fprintf(&b, "%s=%s\n", k, quoteDotenv(p.Variables[k]))
	}
	return []byte(b.String())
}

func quoteDotenv(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "$", `\$`)
	return `"` + r.Replace(v) + `"`
}
