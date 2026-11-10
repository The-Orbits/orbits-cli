package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newSandboxesCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandboxes",
		Aliases: []string{"sandbox", "sb"},
		Short:   "Start, stop and remove the project's sandboxes",
	}
	cmd.AddCommand(newSandboxesListCmd(a), newSandboxCreateCmd(a), newSandboxActionCmd(a, "start"), newSandboxActionCmd(a, "stop"), newSandboxRemoveCmd(a))
	return cmd
}

func newSandboxesListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the project's sandboxes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			page, err := c.Sandboxes.List(ctx(cmd), team, project)
			if err != nil {
				return err
			}
			if printList(a, page.Items, func(s orbits.Sandbox) string { return s.Name }, "No sandboxes.", "Start one", "orbits sandboxes create <name>") {
				return nil
			}
			rows := make([][]string, len(page.Items))
			for i, s := range page.Items {
				rows[i] = []string{s.Name, state(a.out, string(s.State)), string(s.Runtime), string(s.Size), or(str(s.Source)), s.URL, output.Ago(s.StartedAt)}
			}
			a.out.Table([]string{"sandbox", "state", "runtime", "size", "source", "url", "started"}, rows)
			a.out.Summary(fmt.Sprintf("%d of %d running", page.Running, page.Limit))
			return nil
		},
	}
}

func newSandboxCreateCmd(a *app) *cobra.Command {
	var runtime, size string
	var clone, env bool
	var stopAfter int
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Start a new sandbox",
		Example: `  orbits sandboxes create try-upgrade --runtime node22 --clone --env`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := oneOf("runtime", runtime, orbits.SandboxRuntimeNode22, orbits.SandboxRuntimePython312, orbits.SandboxRuntimeGo123)
			if err != nil {
				return &usageError{err, cmd}
			}
			sz, err := oneOf("size", size, orbits.SandboxSizeSmall, orbits.SandboxSizeLarge)
			if err != nil {
				return &usageError{err, cmd}
			}
			switch stopAfter {
			case 10, 30, 60, 120:
			default:
				return &usageError{fmt.Errorf("--stop-after must be 10, 30, 60 or 120 minutes"), cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			s, err := c.Sandboxes.Create(ctx(cmd), team, project, orbits.SandboxCreate{Name: args[0], Runtime: rt, Size: sz, Clone: &clone, Env: &env, StopAfter: &stopAfter})
			if err != nil {
				return err
			}
			if printValue(a, s, s.Name) {
				return nil
			}
			a.out.Success("Starting sandbox %s: %s", s.Name, s.URL)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&runtime, "runtime", "node22", "node22, python312 or go123")
	f.StringVar(&size, "size", "small", "small or large")
	f.BoolVar(&clone, "clone", false, "check out the project's repository at main")
	f.BoolVar(&env, "env", false, "copy the Preview environment variables in")
	f.IntVar(&stopAfter, "stop-after", 30, "idle minutes before it suspends: 10, 30, 60 or 120")
	return cmd
}

func newSandboxActionCmd(a *app, action string) *cobra.Command {
	short := "Start a stopped sandbox"
	if action == "stop" {
		short = "Stop a sandbox (it keeps its files)"
	}
	return &cobra.Command{
		Use:   action + " <name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			var s *orbits.Sandbox
			if action == "stop" {
				s, err = c.Sandboxes.Stop(ctx(cmd), team, project, args[0])
			} else {
				s, err = c.Sandboxes.Start(ctx(cmd), team, project, args[0])
			}
			if err != nil {
				return err
			}
			if printValue(a, s, s.Name) {
				return nil
			}
			a.out.Success("%s: %s.", s.Name, s.State)
			return nil
		},
	}
}

func newSandboxRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete a sandbox and its files",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Delete sandbox %s and its files?", args[0])); err != nil {
				return err
			}
			if err := c.Sandboxes.Delete(ctx(cmd), team, project, args[0]); err != nil {
				return err
			}
			if printValue(a, map[string]string{"deleted": args[0]}, args[0]) {
				return nil
			}
			a.out.Success("Deleted sandbox %s.", args[0])
			return nil
		},
	}
}
