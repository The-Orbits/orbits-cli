package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/The-Orbits/orbits-cli/internal/mcpconfig"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

const mcpInstallHint = "Download it from https://github.com/The-Orbits/orbits-mcp/releases, or: go install github.com/The-Orbits/orbits-mcp/cmd/orbits-mcp@latest"

// lookPath and runTool find and run other programs (claude, gemini,
// orbits-mcp); tests replace them.
var (
	lookPath = exec.LookPath
	runTool  = func(c context.Context, name string, args ...string) ([]byte, error) {
		c, cancel := context.WithTimeout(c, 30*time.Second)
		defer cancel()
		return exec.CommandContext(c, name, args...).CombinedOutput()
	}
)

// mcpEnv is where this machine's AI apps keep their config.
func mcpEnv() (mcpconfig.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return mcpconfig.Env{}, fmt.Errorf("finding your home directory: %w", err)
	}
	return mcpconfig.Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}, nil
}

func newMCPCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Connect AI apps to Orbits through MCP",
		Long: `Connect AI apps to Orbits through the Model Context Protocol (MCP), so they
can list, deploy and manage your projects for you.

The hosted server (` + mcpconfig.RemoteURL + `) is the default: the app opens
your browser and you approve it in the web app, choosing its team and access. With
--local the app runs orbits-mcp on this machine instead, with your signed-in
profile or ORBITS_TOKEN.`,
	}
	cmd.AddCommand(newMCPInstallCmd(a), newMCPStatusCmd(a), newMCPListClientsCmd(a))
	return cmd
}

func newMCPInstallCmd(a *app) *cobra.Command {
	var local, printOnly bool
	var serverURL string
	cmd := &cobra.Command{
		Use:   "install <client>",
		Short: "Add Orbits to an AI app",
		Long: `Add Orbits to an AI app: ` + strings.Join(mcpconfig.IDs(), ", ") + `.

The app's config file gains a "orbits" server; every other server and setting
in it is kept, and the old file is saved next to it as <file>.bak. Claude Code
and Gemini CLI are set up with their own commands when installed. For Claude
(the hosted server), ChatGPT and Grok, the steps to follow are printed.

--print shows what would be added and changes nothing.`,
		Example: `  orbits mcp install cursor
  orbits mcp install claude-code
  orbits mcp install claude --local
  orbits mcp install codex --print`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: mcpconfig.IDs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := mcpconfig.Find(args[0])
			if !ok {
				return &usageError{fmt.Errorf("unknown client %q: use one of %s", args[0], strings.Join(mcpconfig.IDs(), ", ")), cmd}
			}
			if local && !c.Local {
				return &usageError{fmt.Errorf("%s can only use the hosted server: run it without --local", c.Name), cmd}
			}
			target := mcpconfig.RemoteURL
			if cmd.Flags().Changed("url") {
				if local {
					return &usageError{errors.New("--url is the hosted server's address: it does not go with --local"), cmd}
				}
				if err := mcpconfig.CheckURL(serverURL); err != nil {
					return &usageError{err, cmd}
				}
				target = serverURL
			}
			mcpconfig.Command = mcpconfig.LocalCommand
			if local {
				path, err := lookPath(mcpconfig.LocalCommand)
				if err != nil {
					a.out.Warn("orbits-mcp is not on your PATH, and %s will need it. %s", c.Name, mcpInstallHint)
				} else if abs, err := filepath.Abs(path); err == nil {
					mcpconfig.Command = abs
				}
			}
			i := &mcpInstall{a: a, cmd: cmd, c: c, local: local, url: target, print: printOnly}
			return i.run()
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "run orbits-mcp on this machine instead of using the hosted server")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the config instead of changing anything")
	cmd.Flags().StringVar(&serverURL, "url", "", "use another hosted server URL (default "+mcpconfig.RemoteURL+")")
	return cmd
}

type mcpInstall struct {
	a     *app
	cmd   *cobra.Command
	c     mcpconfig.Client
	local bool
	url   string
	print bool
}

func (i *mcpInstall) run() error {
	a, c := i.a, i.c
	switch {
	case c.ID == "chatgpt":
		a.out.Line("To add Orbits to ChatGPT:\n\n" +
			"  1. In ChatGPT, open Settings › Connectors › Advanced and turn on developer mode.\n" +
			"  2. Choose Create, name it Orbits, and paste this URL:\n\n" +
			"       " + i.url + "\n\n" +
			"  3. Choose OAuth for authentication, then Create. ChatGPT opens your browser to approve Orbits.")
		return nil
	case c.ID == "grok":
		a.out.Line("Grok uses Orbits through the xAI API's remote MCP tool, with an Orbits API\n" +
			"token instead of a browser sign-in. Make a token under Team › Settings › API\n" +
			"tokens (read only is recommended), set it as ORBITS_TOKEN, and add this\n" +
			"tool to your request:\n\n" +
			"  \"tools\": [{\n" +
			"    \"type\": \"mcp\",\n" +
			"    \"server_url\": \"" + i.url + "\",\n" +
			"    \"server_label\": \"orbits\",\n" +
			"    \"authorization\": \"Bearer $ORBITS_TOKEN\"\n" +
			"  }]\n\n" +
			"Keep the token out of your code: read it from the environment.")
		return nil
	case c.ID == "claude" && !i.local:
		a.out.Line("To add Orbits to Claude (claude.ai or the desktop app):\n\n" +
			"  1. Open Settings › Connectors and choose Add custom connector.\n" +
			"  2. Name it Orbits and paste this URL:\n\n" +
			"       " + i.url + "\n\n" +
			"  3. Choose Add, then Connect. Claude opens your browser to approve Orbits.\n\n" +
			"To run the server on this machine for Claude Desktop instead: orbits mcp install claude --local")
		return nil
	}

	env, err := mcpEnv()
	if err != nil {
		return err
	}
	path := c.Path(env)

	if i.print {
		i.printConfig(path)
		return nil
	}

	if args := i.toolArgs(); args != nil {
		if _, err := lookPath(args[0]); err == nil {
			return i.runTool(args)
		}
		if c.ID == "claude-code" {
			// Claude Code's own file is its state too: change it only
			// when Claude Code is there to read it.
			if _, err := os.Stat(path); err != nil {
				a.out.Note("The claude command is not installed. Install Claude Code, then run:\n")
				a.out.Line("%s", shellJoin(args))
				return nil
			}
		}
	}
	return i.writeFile(path)
}

// toolArgs is the command that adds the server with the app's own CLI, or
// nil when the app has none. User scope, so it works in every project.
func (i *mcpInstall) toolArgs() []string {
	switch i.c.ID {
	case "claude-code":
		if i.local {
			return []string{"claude", "mcp", "add", "--scope", "user", mcpconfig.ServerName, "--", mcpconfig.Command, "stdio"}
		}
		return []string{"claude", "mcp", "add", "--transport", "http", "--scope", "user", mcpconfig.ServerName, i.url}
	case "gemini":
		if i.local {
			return []string{"gemini", "mcp", "add", "--scope", "user", mcpconfig.ServerName, mcpconfig.Command, "stdio"}
		}
		return []string{"gemini", "mcp", "add", "--scope", "user", "--transport", "http", mcpconfig.ServerName, i.url}
	}
	return nil
}

func (i *mcpInstall) runTool(args []string) error {
	a := i.a
	// "add" refuses a name that is taken: an old orbits entry goes first.
	_, _ = runTool(ctx(i.cmd), args[0], "mcp", "remove", "--scope", "user", mcpconfig.ServerName)
	out, err := runTool(ctx(i.cmd), args[0], args[1:]...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s did not add Orbits: %s\nTry it yourself: %s", args[0], msg, shellJoin(args))
	}
	a.out.Success("Added Orbits to %s (%s).", i.c.Name, shellJoin(args))
	i.next()
	return nil
}

func (i *mcpInstall) writeFile(path string) error {
	a, c := i.a, i.c
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var data []byte
	if c.Format == mcpconfig.TOML {
		data, err = mcpconfig.MergeTOML(old, mcpconfig.TOMLTable(i.local, i.url))
	} else {
		data, err = mcpconfig.MergeJSON(old, c.Key, c.Entry(i.local, i.url))
	}
	if err != nil {
		return fmt.Errorf("could not add Orbits to %s, so it was left as it was: %w\nFix the file, or see what to add: orbits mcp install %s --print", path, err, c.ID)
	}
	if string(data) == string(old) {
		a.out.Success("%s already has Orbits (%s).", c.Name, path)
		i.next()
		return nil
	}
	backup, err := mcpconfig.WriteFile(path, data)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	a.out.Success("Added Orbits to %s (%s).", c.Name, path)
	if backup != "" {
		a.out.Note("The previous file is in %s.", backup)
	}
	i.next()
	return nil
}

func (i *mcpInstall) next() {
	a, c := i.a, i.c
	if c.ID == "codex" && !i.local {
		a.out.Next("Next", "codex mcp login orbits  (opens your browser to approve Orbits)")
		return
	}
	if i.local {
		a.out.Next("Next", fmt.Sprintf("Restart %s; it runs orbits-mcp with your signed-in profile or ORBITS_TOKEN", c.Name))
		return
	}
	a.out.Next("Next", fmt.Sprintf("Open %s; it will open your browser to approve Orbits", c.Name))
}

// printConfig shows the entry, the file it goes in, and any shortcut the
// app offers. Results on stdout, labels on stderr.
func (i *mcpInstall) printConfig(path string) {
	a, c := i.a, i.c
	if args := i.toolArgs(); args != nil {
		a.out.Note("Run:\n")
		a.out.Line("%s\n", shellJoin(args))
		a.out.Note("or add this to %s:\n", path)
	} else {
		a.out.Note("Add this to %s:\n", path)
	}
	if c.Format == mcpconfig.TOML {
		a.out.Line("%s", strings.TrimSuffix(mcpconfig.TOMLTable(i.local, i.url), "\n"))
	} else {
		a.out.Line("%s", strings.TrimSuffix(mcpconfig.Snippet(c.Key, c.Entry(i.local, i.url)), "\n"))
	}
	switch c.ID {
	case "cursor":
		cfg, _ := json.Marshal(c.Entry(i.local, i.url))
		a.out.Note("\nor open this link to add it in Cursor:\n")
		a.out.Line("cursor://anysphere.cursor-deeplink/mcp/install?name=%s&config=%s", mcpconfig.ServerName, url.QueryEscape(base64.StdEncoding.EncodeToString(cfg)))
	case "vscode":
		entry := c.Entry(i.local, i.url)
		entry["name"] = mcpconfig.ServerName
		cfg, _ := json.Marshal(entry)
		a.out.Note("\nor run:\n")
		a.out.Line("code --add-mcp '%s'", cfg)
	case "codex":
		if !i.local {
			a.out.Note("\nthen sign in: codex mcp login orbits")
		}
	}
}

// shellJoin prints a command for a person to copy; quoting only what needs it.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, s := range args {
		if s == "" || strings.ContainsAny(s, " \t'\"$&;|<>*?()[]{}\\`!#~") {
			s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
		out[i] = s
	}
	return strings.Join(out, " ")
}

type mcpClientStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Config     string `json:"config,omitempty"`
	Configured bool   `json:"configured"`
}

type mcpStatus struct {
	HostedURL string `json:"hosted_url"`
	Local     struct {
		Installed bool   `json:"installed"`
		Path      string `json:"path,omitempty"`
		Version   string `json:"version,omitempty"`
	} `json:"local"`
	Clients []mcpClientStatus `json:"clients"`
}

func newMCPStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the hosted server, whether orbits-mcp is installed, and which apps have Orbits",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := mcpEnv()
			if err != nil {
				return err
			}
			var s mcpStatus
			s.HostedURL = mcpconfig.RemoteURL
			if p, err := lookPath(mcpconfig.LocalCommand); err == nil {
				s.Local.Installed, s.Local.Path = true, p
				c, cancel := context.WithTimeout(ctx(cmd), 5*time.Second)
				out, err := runTool(c, p, "version")
				cancel()
				if err == nil {
					s.Local.Version = strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
				}
			}
			for _, c := range mcpconfig.Clients {
				st := mcpClientStatus{ID: c.ID, Name: c.Name}
				if c.Format != mcpconfig.None {
					st.Config = c.Path(env)
					if data, err := os.ReadFile(st.Config); err == nil {
						if c.Format == mcpconfig.TOML {
							st.Configured = mcpconfig.HasTOML(data)
						} else {
							st.Configured = mcpconfig.HasJSON(data, c.Key)
						}
					}
				}
				s.Clients = append(s.Clients, st)
			}

			switch {
			case a.out.JSON:
				a.out.Value(s)
				return nil
			case a.out.Quiet:
				for _, c := range s.Clients {
					if c.Configured {
						a.out.Line("%s", c.ID)
					}
				}
				return nil
			}
			local := "not installed"
			if s.Local.Installed {
				local = s.Local.Path
				if s.Local.Version != "" {
					local = s.Local.Version + "  " + a.out.Dim(s.Local.Path)
				}
			}
			a.out.Print(output.Detail{
				Title:    "Orbits MCP",
				Sections: [][]output.Pair{{{"Hosted", s.HostedURL}, {"orbits-mcp", local}}},
			})
			a.out.Line("")
			rows := [][]string{}
			for _, c := range s.Clients {
				state, where := "—", output.Or(&c.Config)
				if c.Config == "" {
					where = a.out.Dim("set up in the app")
				} else if c.Configured {
					state = a.out.State("enabled")
				}
				rows = append(rows, []string{c.ID, state, where})
			}
			a.out.Table([]string{"client", "orbits", "config"}, rows)
			if !s.Local.Installed {
				a.out.Note("\norbits-mcp is needed only for --local. %s", mcpInstallHint)
			}
			return nil
		},
	}
}

func newMCPListClientsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list-clients",
		Short: "List the AI apps orbits mcp install can set up",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			type row struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Remote bool   `json:"remote"`
				Local  bool   `json:"local"`
			}
			rows := make([]row, len(mcpconfig.Clients))
			for i, c := range mcpconfig.Clients {
				rows[i] = row{c.ID, c.Name, c.Remote, c.Local}
			}
			if printList(a, rows, func(r row) string { return r.ID }, "") {
				return nil
			}
			table := make([][]string, len(rows))
			for i, r := range rows {
				how := "hosted"
				if r.Local {
					how = "hosted, local"
				}
				table[i] = []string{r.ID, r.Name, how}
			}
			a.out.Table([]string{"client", "app", "server"}, table)
			return nil
		},
	}
}
