package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newDeploymentsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deployments",
		Aliases: []string{"deployment", "dpl"},
		Short:   "List and show deployments; promote, roll back, redeploy and cancel them",
	}
	cmd.AddCommand(newDeploymentsListCmd(a), newDeploymentsGetCmd(a), newPromoteCmd(a), newRollbackCmd(a), newRedeployCmd(a), newCancelCmd(a))
	return cmd
}

func newDeploymentsListCmd(a *app) *cobra.Command {
	var env, st, branch string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the project's deployments, newest first",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			params := &orbits.ListDeploymentsParams{Limit: min(max(limit, 1), 100), Branch: branch}
			if env != "" {
				e, err := oneOf("environment", env, orbits.DeploymentEnvironmentProduction, orbits.DeploymentEnvironmentPreview)
				if err != nil {
					return &usageError{err, cmd}
				}
				params.Environment = e
			}
			if st != "" {
				s, err := oneOf("state", st, orbits.DeploymentStateQueued, orbits.DeploymentStateBuilding, orbits.DeploymentStateReady, orbits.DeploymentStateFailed, orbits.DeploymentStateCanceled)
				if err != nil {
					return &usageError{err, cmd}
				}
				params.State = s
			}
			items, err := collect(c.Deployments.All(ctx(cmd), team, project, params), limit)
			if err != nil {
				return err
			}
			if printList(a, items, func(d orbits.Deployment) string { return d.ID }, "No deployments yet.", "Deploy", "orbits deploy") {
				return nil
			}
			rows := make([][]string, len(items))
			for i, d := range items {
				env := string(d.Environment)
				if d.Current {
					env += " " + a.out.Bold("(current)")
				}
				rows[i] = []string{a.out.Dim(d.ID), state(a.out, string(d.State)), env, or(d.Branch), short(d.SHA), trimTo(str(d.Message), 48), output.Seconds(d.DurationSeconds), output.Ago(d.CreatedAt)}
			}
			a.out.Table([]string{"deployment", "state", "environment", "branch", "commit", "message", "took", "created"}, rows)
			a.out.Summary(count(len(items), "deployment"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&env, "environment", "", "production or preview")
	f.StringVar(&st, "state", "", "queued, building, ready, failed or canceled")
	f.StringVar(&branch, "branch", "", "only this branch's")
	f.IntVar(&limit, "limit", 20, "at most this many")
	return cmd
}

// deploymentRef is the deployment named, or the project's newest.
func (a *app) deploymentRef(cmd *cobra.Command, c *orbits.Client, team, project, ref string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	page, err := c.Deployments.List(ctx(cmd), team, project, &orbits.ListDeploymentsParams{Limit: 1})
	if err != nil {
		return "", err
	}
	if len(page.Items) == 0 {
		return "", errors.New("the project has no deployments yet: orbits deploy")
	}
	return page.Items[0].ID, nil
}

func newDeploymentsGetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "get [deployment]",
		Aliases: []string{"show", "inspect"},
		Short:   "Show a deployment (default: the newest)",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			id, err := a.deploymentRef(cmd, c, team, project, argOr(args))
			if err != nil {
				return err
			}
			d, err := c.Deployments.Get(ctx(cmd), team, project, id)
			if err != nil {
				return err
			}
			if printValue(a, d, d.ID) {
				return nil
			}
			var steps []string
			for _, s := range d.Steps {
				steps = append(steps, fmt.Sprintf("%s %s", stepLabel(s.Key), a.out.Dim(output.Seconds(s.Seconds))))
			}
			var domains []string
			for _, dom := range d.Domains {
				domains = append(domains, dom.Name)
			}
			env := string(d.Environment)
			if d.Current {
				env += " (current)"
			}
			sections := [][]output.Pair{
				{{"URL", or(str(d.URL))}, {"Environment", env}, {"Domains", or(strings.Join(domains, ", "))}},
				{{"Branch", or(d.Branch)}, {"Commit", short(d.SHA) + "  " + str(d.Message)}, {"Trigger", string(d.Trigger)}},
				{{"Steps", or(strings.Join(steps, " · "))}, {"Took", output.Seconds(d.DurationSeconds)}, {"Created", output.When(d.CreatedAt)}},
			}
			if f := d.Failure; f != nil {
				sections = append(sections, []output.Pair{{"Failure", f.Title + ": " + f.Message}})
			}
			a.out.Print(output.Detail{
				Title: d.Project.Name, State: a.out.State(string(d.State)), Subtitle: d.ID, Sections: sections,
				Next: [][2]string{{"Build log", "orbits logs " + d.ID}},
			})
			return nil
		},
	}
}

func newPromoteCmd(a *app) *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "promote <deployment>",
		Short: "Rebuild a preview for production; it goes live when ready",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Promote %s? It is rebuilt with production's variables and replaces production when ready.", args[0])); err != nil {
				return err
			}
			d, err := c.Deployments.Promote(ctx(cmd), team, project, args[0])
			if err != nil {
				return err
			}
			p, err := c.Projects.Get(ctx(cmd), team, project)
			if err != nil {
				return err
			}
			return a.afterQueued(cmd, c, team, p, d, noWait, true)
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "do not follow the build")
	return cmd
}

func newRollbackCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback [deployment]",
		Short: "Point production at an earlier production deployment, at once",
		Long: `Point production at an earlier production deployment straight away, with no
rebuild (default: the one before the current one). Automatic production
deploys pause until a promote, so the next push does not replace it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			target := argOr(args)
			live := true
			page, err := c.Deployments.List(ctx(cmd), team, project, &orbits.ListDeploymentsParams{Limit: 20, Environment: orbits.DeploymentEnvironmentProduction, Live: &live})
			if err != nil {
				return err
			}
			current := ""
			if page.Current != nil {
				current = page.Current.ID
			}
			if target == "" {
				for _, d := range page.Items {
					if d.ID != current && d.State == orbits.DeploymentStateReady {
						target = d.ID
						break
					}
				}
				if target == "" {
					return errors.New("there is no earlier production deployment to roll back to")
				}
			}
			if target == current {
				return fmt.Errorf("%s is what production serves already", target)
			}
			if err := a.confirm(fmt.Sprintf("Roll production back to %s? It serves it at once, and automatic production deploys pause.", target)); err != nil {
				return err
			}
			r, err := c.Deployments.Rollback(ctx(cmd), team, project, target)
			if err != nil {
				return err
			}
			if printValue(a, r, r.Deployment.ID) {
				return nil
			}
			a.out.Success("Production now serves %s.", r.Deployment.ID)
			if current != "" {
				a.out.Next("Undo", "orbits deployments rollback "+current)
			}
			a.out.Next("Resume automatic deploys", "orbits deployments promote <deployment>")
			return nil
		},
	}
}

func newRedeployCmd(a *app) *cobra.Command {
	var noCache, noWait bool
	cmd := &cobra.Command{
		Use:   "redeploy [deployment]",
		Short: "Build a deployment's commit (or upload) again (default: production's)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			target := argOr(args)
			if target == "" {
				page, err := c.Deployments.List(ctx(cmd), team, project, &orbits.ListDeploymentsParams{Limit: 1})
				if err != nil {
					return err
				}
				switch {
				case page.Current != nil:
					target = page.Current.ID
				case len(page.Items) > 0:
					target = page.Items[0].ID
				default:
					return errors.New("the project has no deployments to rebuild: orbits deploy")
				}
			}
			req := orbits.RedeployRequest{}
			if noCache {
				req.UseCache = orbits.Ptr(false)
			}
			d, err := c.Deployments.Redeploy(ctx(cmd), team, project, target, req)
			if err != nil {
				return err
			}
			p, err := c.Projects.Get(ctx(cmd), team, project)
			if err != nil {
				return err
			}
			return a.afterQueued(cmd, c, team, p, d, noWait, true)
		},
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "build without the build cache")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "do not follow the build")
	return cmd
}

func newCancelCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <deployment>",
		Short: "Stop a queued or building deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Cancel deployment %s?", args[0])); err != nil {
				return err
			}
			d, err := c.Deployments.Cancel(ctx(cmd), team, project, args[0])
			if err != nil {
				return err
			}
			if printValue(a, d, d.ID) {
				return nil
			}
			if d.State == orbits.DeploymentStateCanceled {
				a.out.Success("Canceled %s.", d.ID)
			} else {
				a.out.Success("Stopping %s: its builder stops within a few seconds.", d.ID)
			}
			return nil
		},
	}
}

func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
