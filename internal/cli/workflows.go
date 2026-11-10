package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newWorkflowsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workflows",
		Aliases: []string{"workflow", "wf"},
		Short:   "List workflows and their runs; run, cancel and retry them",
	}
	cmd.AddCommand(newWorkflowsListCmd(a), newWorkflowRunsCmd(a), newWorkflowRunCmd(a), newWorkflowRunActionCmd(a, "cancel"), newWorkflowRunActionCmd(a, "retry"))
	return cmd
}

func newWorkflowsListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the project's workflows",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			page, err := c.Workflows.List(ctx(cmd), team, project)
			if err != nil {
				return err
			}
			if printList(a, page.Workflows, func(w orbits.Workflow) string { return w.Name }, "No workflows: they appear once a deployment defines them.") {
				return nil
			}
			rows := make([][]string, len(page.Workflows))
			for i, w := range page.Workflows {
				last := "—"
				if w.LastRun != nil {
					last = a.out.State(string(w.LastRun.State)) + " " + a.out.Dim(output.Ago(w.LastRun.At))
				}
				rate := "—"
				if w.SuccessRate != nil {
					rate = fmt.Sprintf("%.0f%%", *w.SuccessRate*100)
				}
				rows[i] = []string{w.Name, or(w.TriggerText), last, rate}
			}
			a.out.Table([]string{"workflow", "trigger", "last run", "success"}, rows)
			return nil
		},
	}
}

func newWorkflowRunsCmd(a *app) *cobra.Command {
	var workflow string
	return withFlag(&cobra.Command{
		Use:   "runs [run id]",
		Short: "List recent runs, or show one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if len(args) == 1 {
				r, err := c.Workflows.GetRun(ctx(cmd), team, project, args[0])
				if err != nil {
					return err
				}
				return a.printRun(r)
			}
			page, err := c.Workflows.List(ctx(cmd), team, project)
			if err != nil {
				return err
			}
			var runs []orbits.WorkflowRun
			for _, r := range page.Runs {
				if workflow == "" || r.Workflow == workflow {
					runs = append(runs, r)
				}
			}
			if printList(a, runs, func(r orbits.WorkflowRun) string { return r.ID }, "No runs yet.") {
				return nil
			}
			rows := make([][]string, len(runs))
			for i, r := range runs {
				rows[i] = []string{a.out.Dim(r.ID), r.Workflow, state(a.out, string(r.State)), or(r.TriggerText), output.Seconds(&r.Seconds), output.Ago(r.StartedAt)}
			}
			a.out.Table([]string{"run", "workflow", "state", "trigger", "took", "started"}, rows)
			return nil
		},
	}, func(c *cobra.Command) { c.Flags().StringVar(&workflow, "workflow", "", "only this workflow's runs") })
}

func (a *app) printRun(r *orbits.WorkflowRun) error {
	if printValue(a, r, r.ID) {
		return nil
	}
	sections := [][]output.Pair{
		{{"Workflow", r.Workflow}, {"Trigger", or(r.TriggerText)}, {"Attempts", or(r.Attempts)}},
		{{"Started", output.When(r.StartedAt)}, {"Finished", output.WhenPtr(r.FinishedAt)}, {"Took", output.Seconds(&r.Seconds)}},
	}
	if len(r.Error) > 0 {
		sections = append(sections, []output.Pair{{"Error", strings.Join(r.Error, "\n")}})
	}
	a.out.Print(output.Detail{Title: r.Workflow, State: a.out.State(string(r.State)), Subtitle: r.ID, Sections: sections})
	return nil
}

func newWorkflowRunCmd(a *app) *cobra.Command {
	var input string
	return withFlag(&cobra.Command{
		Use:     "run <workflow>",
		Short:   "Start a workflow now",
		Example: `  orbits workflows run send-digest --input '{"week":45}'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := orbits.RunWorkflowRequest{}
			if input != "" {
				if !json.Valid([]byte(input)) {
					return &usageError{fmt.Errorf("--input must be JSON"), cmd}
				}
				req.Input = json.RawMessage(input)
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			r, err := c.Workflows.Run(ctx(cmd), team, project, args[0], req)
			if err != nil {
				return err
			}
			if printValue(a, r, r.ID) {
				return nil
			}
			a.out.Success("Started %s (%s).", r.Workflow, r.ID)
			a.out.Next("See it", "orbits workflows runs "+r.ID)
			return nil
		},
	}, func(c *cobra.Command) { c.Flags().StringVar(&input, "input", "", "the JSON the run starts with") })
}

func newWorkflowRunActionCmd(a *app, action string) *cobra.Command {
	short := "Cancel a running workflow run"
	if action == "retry" {
		short = "Run a failed workflow run again"
	}
	return &cobra.Command{
		Use:   action + " <run id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			var r *orbits.WorkflowRun
			if action == "cancel" {
				if err := a.confirm(fmt.Sprintf("Cancel run %s?", args[0])); err != nil {
					return err
				}
				r, err = c.Workflows.CancelRun(ctx(cmd), team, project, args[0])
			} else {
				r, err = c.Workflows.RetryRun(ctx(cmd), team, project, args[0])
			}
			if err != nil {
				return err
			}
			if printValue(a, r, r.ID) {
				return nil
			}
			a.out.Success("%s: %s.", r.ID, r.State)
			return nil
		},
	}
}

func withFlag(cmd *cobra.Command, add func(*cobra.Command)) *cobra.Command {
	add(cmd)
	return cmd
}
