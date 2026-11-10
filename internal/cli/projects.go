package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newProjectsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "List, show, create, delete, pause and resume projects",
	}
	cmd.AddCommand(newProjectsListCmd(a), newProjectsGetCmd(a), newProjectsCreateCmd(a), newProjectsDeleteCmd(a),
		newProjectsPauseCmd(a, true), newProjectsPauseCmd(a, false))
	return cmd
}

func newProjectsListCmd(a *app) *cobra.Command {
	var search string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the team's projects, most recently deployed first",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			projects, err := collect(c.Projects.All(ctx(cmd), team, &orbits.ListProjectsParams{Q: search, Limit: 100}), limit)
			if err != nil {
				return err
			}
			if printList(a, projects, func(p orbits.Project) string { return p.Slug }, "No projects yet.", "Make one", "orbits projects create <name>") {
				return nil
			}
			rows := make([][]string, len(projects))
			for i, p := range projects {
				last := "—"
				if d := p.LatestDeployment; d != nil {
					last = output.Ago(d.CreatedAt)
				}
				rows[i] = []string{p.Slug, state(a.out, string(p.Status)), string(p.Workload.Type), sourceKind(p), or(str(p.ProductionURL)), last}
			}
			a.out.Table([]string{"project", "status", "workload", "source", "production", "deployed"}, rows)
			a.out.Summary(count(len(projects), "project"))
			return nil
		},
	}
	cmd.Flags().StringVar(&search, "search", "", "only projects whose name matches")
	cmd.Flags().IntVar(&limit, "limit", 0, "at most this many (default: all)")
	return cmd
}

func newProjectsGetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "get [project]",
		Aliases: []string{"show"},
		Short:   "Show a project (default: the linked one)",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, ref, err := a.clientAndProject(cmd, argOr(args))
			if err != nil {
				return err
			}
			p, err := c.Projects.Get(ctx(cmd), team, ref)
			if err != nil {
				return err
			}
			if printValue(a, p, p.ID) {
				return nil
			}
			a.printProject(p, team)
			return nil
		},
	}
}

func (a *app) printProject(p *orbits.Project, team string) {
	source := sourceKind(*p)
	if p.Source != nil && p.Source.ProductionBranch != "" {
		source += " · " + p.Source.ProductionBranch
	}
	last := "—"
	if d := p.LatestDeployment; d != nil {
		last = fmt.Sprintf("%s  %s  %s", d.ID, a.out.State(string(d.State)), a.out.Dim(output.Ago(d.CreatedAt)))
	}
	paused := p.Paused != nil && *p.Paused
	d := output.Detail{
		Title:    p.Name,
		State:    a.out.State(string(p.Status)),
		Subtitle: p.ID,
		Sections: [][]output.Pair{
			{{"Production", or(str(p.ProductionURL))}, {"Address", p.FreeAddress}},
			{{"Workload", string(p.Workload.Type)}, {"Source", source}, {"Region", p.Region}, {"Paused", yesNo(paused)}},
			{{"Latest", last}, {"Created", output.When(p.CreatedAt)}},
		},
		Next: [][2]string{{"Deploy", "orbits deploy"}, {"Open", "orbits open"}},
	}
	a.out.Print(d)
}

func newProjectsCreateCmd(a *app) *cobra.Command {
	var repo, branch, root, workload, outputDir, region string
	var spa bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Make a project: an upload project, or one built from a GitHub repository",
		Long: `Make a project. Without --repo it is an upload project, deployed with
"orbits deploy" from a folder; with --repo, Orbits builds the repository on
every push (the team's GitHub must be connected in the web app).`,
		Example: `  orbits projects create storefront
  orbits projects create docs --workload static --output-dir dist --spa
  orbits projects create api --repo acme/api --branch main`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			req := orbits.ProjectCreate{Name: args[0]}
			if repo != "" {
				req.Source = &orbits.Source{Provider: orbits.SourceProviderGithub, Repository: repo, ProductionBranch: branch}
				if root != "" {
					req.Source.RootDirectory = &root
				}
			} else {
				kind := orbits.ProjectSourceKindUpload
				req.SourceKind = &kind
				if branch != "main" || root != "" {
					return &usageError{fmt.Errorf("--branch and --root are for a project with --repo"), cmd}
				}
			}
			if workload != "" {
				w, err := oneOf("workload", workload, orbits.WorkloadTypeStatic, orbits.WorkloadTypeFunction, orbits.WorkloadTypeWorker, orbits.WorkloadTypeApp, orbits.WorkloadTypeContainer)
				if err != nil {
					return &usageError{err, cmd}
				}
				req.Workload = &orbits.Workload{Type: w}
			}
			if outputDir != "" || spa {
				if req.Workload == nil || req.Workload.Type != orbits.WorkloadTypeStatic {
					return &usageError{fmt.Errorf("--output-dir and --spa are for --workload static"), cmd}
				}
				if outputDir != "" {
					req.Workload.OutputDirectory = &outputDir
				}
				if spa {
					req.Workload.SPAFallback = &spa
				}
			}
			if region != "" {
				req.Region = &region
			}
			p, err := c.Projects.Create(ctx(cmd), team, req)
			if err != nil {
				return err
			}
			if printValue(a, p, p.ID) {
				return nil
			}
			a.out.Success("Made project %s (%s).", a.out.Bold(p.Name), p.FreeAddress)
			if isUpload(p) {
				a.out.Next("Deploy a folder", "orbits link "+p.Slug+" && orbits deploy")
			} else {
				a.out.Next("Follow its first build", "orbits logs --project "+p.Slug+" -f")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "build this GitHub repository, owner/name")
	f.StringVar(&branch, "branch", "main", "with --repo: the production branch")
	f.StringVar(&root, "root", "", "with --repo: the folder in the repository to build")
	f.StringVar(&workload, "workload", "", "static, function, worker, app or container (default: detected)")
	f.StringVar(&outputDir, "output-dir", "", "for a static site: the folder the build writes")
	f.BoolVar(&spa, "spa", false, "for a static site: serve index.html for paths with no file")
	f.StringVar(&region, "region", "", "region (default: abj-1)")
	return cmd
}

func newProjectsDeleteCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <project>",
		Aliases: []string{"rm"},
		Short:   "Delete a project, its deployments, variables and flags",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, ref, err := a.clientAndProject(cmd, args[0])
			if err != nil {
				return err
			}
			p, err := c.Projects.Get(ctx(cmd), team, ref)
			if err != nil {
				return err
			}
			question := fmt.Sprintf("Delete project %s (%s)?", p.Slug, p.ID)
			if impact, err := c.Projects.GetDeletionImpact(ctx(cmd), team, p.ID); err == nil {
				var parts []string
				for _, x := range []struct {
					n    int
					noun string
				}{{impact.Deployments, "deployment"}, {impact.EnvironmentVariables, "environment variable"}, {impact.Flags, "flag"}} {
					if x.n > 0 {
						parts = append(parts, count(x.n, x.noun))
					}
				}
				if len(parts) > 0 {
					question += " Its " + strings.Join(parts, ", ") + " go with it."
				}
				if impact.Domains > 0 {
					question += fmt.Sprintf(" %s will be detached, not deleted.", count(impact.Domains, "domain"))
				}
			}
			question += " This can't be undone."
			if err := a.confirm(question); err != nil {
				return err
			}
			op, err := c.Projects.Delete(ctx(cmd), team, p.ID)
			if err != nil {
				return err
			}
			steps := a.out.Steps()
			steps.Start("Deleting " + p.Slug)
			if op != nil && op.ID != "" && !op.State.Done() {
				if op, err = c.Operations.Wait(ctx(cmd), op.ID, &orbits.WaitOptions{Interval: pollInterval}); err != nil {
					steps.Fail("", "")
					return err
				}
			} else if op != nil && op.State == orbits.OperationStateFailed {
				steps.Fail("", "")
				return &orbits.OperationError{Operation: op}
			}
			steps.Done("Deleted "+p.Slug, "")
			if printValue(a, op, p.ID) {
				return nil
			}
			if l, err := a.findLink(); err == nil && l.ProjectID == p.ID {
				a.out.Note("This folder is still linked to it: orbits unlink")
			}
			return nil
		},
	}
}

func newProjectsPauseCmd(a *app, pause bool) *cobra.Command {
	use, short := "resume [project]", "Serve a paused project again"
	if pause {
		use, short = "pause [project]", "Stop serving a project (its address answers that it is paused)"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, ref, err := a.clientAndProject(cmd, argOr(args))
			if err != nil {
				return err
			}
			var p *orbits.Project
			if pause {
				if err := a.confirm(fmt.Sprintf("Pause %s? Its production stops serving until you resume it.", ref)); err != nil {
					return err
				}
				p, err = c.Projects.Pause(ctx(cmd), team, ref)
			} else {
				p, err = c.Projects.Resume(ctx(cmd), team, ref)
			}
			if err != nil {
				return err
			}
			if printValue(a, p, p.ID) {
				return nil
			}
			if pause {
				a.out.Success("Paused %s.", p.Slug)
				a.out.Next("Resume it", "orbits projects resume "+p.Slug)
			} else {
				a.out.Success("Resumed %s.", p.Slug)
			}
			return nil
		},
	}
}

func argOr(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}
