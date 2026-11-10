package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

// uploadIgnore is left out of every upload on top of orbits-go's defaults:
// the local variable files "orbits env pull" writes, which hold secrets and
// are never meant to be shipped.
var uploadIgnore = []string{".env.local", ".env.*.local"}

func newDeployCmd(a *app) *cobra.Command {
	var prod, noWait, noLogs bool
	var branch string
	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Deploy the linked project: upload a folder, or build a branch",
		Long: `Deploy the linked project (or --project) and follow the build.

An upload project takes the folder (default: this one) as its source: it is
zipped, leaving out node_modules, .git, .next, .orbits, .env*.local files and
the patterns in .orbitsignore (.gitignore syntax), sent, and built. Uploads go
straight to production.

A Git project builds a branch's newest commit: --prod for the production
branch, --branch for a preview of another. Pushing to the repository deploys
too; "orbits deployments redeploy" builds an existing deployment again.

The build's steps and log are shown as it runs, on stderr, and the address
is printed on stdout when it is ready. A failed build exits with code 5.`,
		Example: `  orbits deploy
  orbits deploy ./dist
  orbits deploy --prod
  orbits deploy --branch feature/checkout
  url=$(orbits deploy --no-logs)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, ref, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			p, err := c.Projects.Get(ctx(cmd), team, ref)
			if err != nil {
				return err
			}
			var d *orbits.Deployment
			if isUpload(p) {
				if branch != "" {
					return &usageError{fmt.Errorf("%s is an upload project: it has no branches, and --branch is for a Git project", p.Slug), cmd}
				}
				dir := "."
				if len(args) == 1 {
					dir = args[0]
				}
				if d, err = a.upload(cmd, c, team, p, dir); err != nil {
					return err
				}
			} else {
				if len(args) == 1 {
					return &usageError{fmt.Errorf("%s builds from %s, not from a folder: push to deploy, or run orbits deploy --prod (or --branch <name>) to build a branch's newest commit", p.Slug, sourceKind(*p)), cmd}
				}
				if d, err = a.deployBranch(cmd, c, team, p, branch, prod); err != nil {
					return err
				}
			}
			return a.afterQueued(cmd, c, team, p, d, noWait, !noLogs)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&prod, "prod", false, "deploy to production (a Git project's production branch; uploads always are)")
	f.StringVar(&branch, "branch", "", "for a Git project: build this branch's newest commit (a preview, unless it is the production branch)")
	f.BoolVar(&noWait, "no-wait", false, "print the deployment's id once it is queued, without following the build")
	f.BoolVar(&noLogs, "no-logs", false, "show the build's steps but not its log lines")
	return cmd
}

func (a *app) upload(cmd *cobra.Command, c *orbits.Client, team string, p *orbits.Project, dir string) (*orbits.Deployment, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &usageError{fmt.Errorf("%s is not a folder: deploy a folder, such as . or ./dist", dir), cmd}
	}
	abs, _ := filepath.Abs(dir)
	steps := a.out.Steps()
	steps.Start("Packing " + filepath.Base(abs))
	files, bytes := 0, int64(0)
	sent := false
	d, err := c.Deployments.UploadDir(ctx(cmd), team, p.ID, dir, &orbits.UploadOptions{
		Ignore: uploadIgnore,
		OnFile: func(string, int64) { files++ },
		OnProgress: func(done, total int64) {
			if !sent {
				sent = true
				steps.Done(fmt.Sprintf("Packed %s", count(files, "file")), output.Bytes(total))
				steps.Start("Uploading")
			}
			bytes = total
		},
	})
	if err != nil {
		steps.Fail("", "")
		return nil, err
	}
	steps.Done("Uploaded", output.Bytes(bytes))
	return d, nil
}

func (a *app) deployBranch(cmd *cobra.Command, c *orbits.Client, team string, p *orbits.Project, branch string, prod bool) (*orbits.Deployment, error) {
	production := "main"
	if p.Source != nil && p.Source.ProductionBranch != "" {
		production = p.Source.ProductionBranch
	}
	switch {
	case branch != "" && prod && branch != production:
		return nil, &usageError{fmt.Errorf("--prod deploys the production branch (%s), not %s", production, branch), cmd}
	case branch == "" && prod:
		branch = production
	case branch == "":
		if !a.interactive() && !a.yes {
			return nil, &usageError{fmt.Errorf("%s builds from %s, so there is no folder to upload. Choose what to build:\n"+
				"  orbits deploy --prod              the newest commit of %s, to production\n"+
				"  orbits deploy --branch <name>     a branch's newest commit, as a preview\n"+
				"  orbits deployments redeploy       build the current production deployment again", p.Slug, sourceKind(*p), production), cmd}
		}
		if err := a.confirm(fmt.Sprintf("%s builds from %s. Deploy the newest commit of %s to production?", p.Slug, sourceKind(*p), production)); err != nil {
			return nil, err
		}
		branch = production
	}
	return c.Deployments.Create(ctx(cmd), team, p.ID, orbits.CreateDeploymentRequest{Branch: &branch})
}

// afterQueued follows a queued deployment to its end, or reports it queued.
func (a *app) afterQueued(cmd *cobra.Command, c *orbits.Client, team string, p *orbits.Project, d *orbits.Deployment, noWait, lines bool) error {
	inspect := a.inspectURL(team, p.Slug, d.ID)
	if noWait {
		if printValue(a, d, d.ID) {
			return nil
		}
		a.out.Line("%s", d.ID)
		a.out.Note("Queued %s (%s).", d.ID, d.Environment)
		a.out.Next("Follow it", "orbits logs "+d.ID+" -f")
		return nil
	}
	a.out.Note("%s %s · %s", a.out.Dim("Deployment"), d.ID, inspect)
	detail, err := a.followBuild(cmd, c, team, p.ID, d.ID, lines)
	if err != nil {
		return err
	}
	if printValue(a, detail, detail.ID) {
		return nil
	}
	address := str(detail.URL)
	if detail.Environment == orbits.DeploymentEnvironmentProduction {
		if pu := str(p.ProductionURL); pu != "" {
			address = pu
		} else if len(detail.Domains) > 0 {
			address = "https://" + detail.Domains[0].Name
		} else if p.FreeAddress != "" {
			address = "https://" + p.FreeAddress
		}
	}
	a.out.Success("Ready in %s (%s)", output.Seconds(detail.DurationSeconds), detail.Environment)
	for _, w := range detail.Warnings {
		a.out.Warn("%s", warningText(w))
	}
	if address != "" {
		a.out.Line("%s", address)
	}
	return nil
}

func (a *app) inspectURL(team, project, deployment string) string {
	return a.webURL() + "/" + url.PathEscape(a.teamSlugFor(team)) + "/" + url.PathEscape(project) + "/deployments/" + url.PathEscape(deployment)
}

// teamSlugFor is the slug for a team ref when it is known, for web links.
func (a *app) teamSlugFor(team string) string {
	if a.meTeam != "" {
		return a.meTeam
	}
	return team
}

var stepLabels = map[orbits.DeploymentStepKey]string{
	"queued": "Queued", "cloning": "Cloning", "installing": "Installing",
	"building": "Building", "uploading": "Uploading", "going_live": "Going live",
}

func stepLabel(k orbits.DeploymentStepKey) string {
	if l, ok := stepLabels[k]; ok {
		return l
	}
	return string(k)
}

func viewSteps(steps []orbits.DeploymentStep) []output.Step {
	out := make([]output.Step, len(steps))
	for i, s := range steps {
		out[i] = output.Step{Key: string(s.Key), Label: stepLabel(s.Key), State: string(s.State), Seconds: s.Seconds}
	}
	return out
}

// buildFailed is a deployment that ended failed or canceled; main exits 5.
type buildFailed struct {
	d *orbits.DeploymentDetail
}

func (e *buildFailed) Error() string {
	d := e.d
	if d.State == orbits.DeploymentStateCanceled {
		return fmt.Sprintf("deployment %s was canceled\nNothing it built went live.", d.ID)
	}
	msg := fmt.Sprintf("deployment %s failed", d.ID)
	if f := d.Failure; f != nil {
		msg = fmt.Sprintf("%s: %s at %s", f.Title, d.ID, stepLabel(f.Step))
		if f.Message != "" {
			msg += "\n" + f.Message
		}
		if f.Line != nil {
			msg += fmt.Sprintf("\nSee: orbits logs %s (line %d)", d.ID, *f.Line)
			return msg
		}
	}
	return msg + "\nSee: orbits logs " + d.ID
}

func (e *buildFailed) Unwrap() error { return &orbits.DeploymentError{Deployment: e.d} }

// followBuild streams a deployment's build: the steps as they change, the
// log lines (if lines), until it ends. It returns the finished deployment,
// or a *buildFailed for one that failed or was canceled.
func (a *app) followBuild(cmd *cobra.Command, c *orbits.Client, team, project, deployment string, lines bool) (*orbits.DeploymentDetail, error) {
	view := a.out.Build(lines)
	var done *orbits.LogStreamDone
	var streamErr error
	for ev, err := range c.Deployments.StreamLogs(ctx(cmd), team, project, deployment, nil) {
		if err != nil {
			streamErr = err
			break
		}
		switch ev.Type {
		case orbits.LogEventLine:
			view.Line(ev.Line.Text, string(ev.Line.Tone))
		case orbits.LogEventSteps:
			view.Steps(viewSteps(ev.Steps))
		case orbits.LogEventDone:
			done = ev.Done
		}
	}
	if ctx(cmd).Err() != nil {
		view.Close()
		return nil, ctx(cmd).Err()
	}
	var d *orbits.DeploymentDetail
	var err error
	if streamErr != nil && !orbits.IsNotFound(streamErr) {
		// The stream broke for good: the build goes on, so wait for its end.
		a.out.Warn("Lost the build log (%v); waiting for the build to finish.", streamErr)
		d, err = c.Deployments.Wait(ctx(cmd), team, project, deployment, &orbits.WaitOptions{Interval: pollInterval})
	} else if streamErr != nil {
		view.Close()
		return nil, streamErr
	} else {
		d, err = c.Deployments.Get(ctx(cmd), team, project, deployment)
		if err == nil && done != nil && done.State != d.State && done.State.Done() {
			d.State, d.Failure = done.State, done.Failure
			if done.DurationSeconds != nil {
				d.DurationSeconds = done.DurationSeconds
			}
		}
	}
	if d != nil {
		view.Steps(viewSteps(d.Steps))
	}
	view.Close()
	var de *orbits.DeploymentError
	if errors.As(err, &de) {
		return nil, &buildFailed{de.Deployment}
	}
	if err != nil {
		return nil, err
	}
	if d.State == orbits.DeploymentStateFailed || d.State == orbits.DeploymentStateCanceled {
		return nil, &buildFailed{d}
	}
	if !d.State.Done() {
		// The stream ended without saying how (it should not): wait for it.
		if d, err = c.Deployments.Wait(ctx(cmd), team, project, deployment, &orbits.WaitOptions{Interval: pollInterval, Timeout: time.Hour}); err != nil {
			if errors.As(err, &de) {
				return nil, &buildFailed{de.Deployment}
			}
			return nil, err
		}
	}
	return d, nil
}

func warningText(w orbits.DeploymentWarning) string {
	if w.Message != "" {
		return w.Message
	}
	if len(w.Keys) > 0 {
		return fmt.Sprintf("The build read variables that were not set: %v. Add them (orbits env add) and redeploy.", w.Keys)
	}
	return string(w.Kind)
}
