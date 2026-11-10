package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

// runtimePoll is how often logs --runtime -f asks for new entries.
var runtimePoll = 2 * time.Second

func newLogsCmd(a *app) *cobra.Command {
	var follow, runtime, timestamps bool
	var since, level, source, search string
	var limit int
	cmd := &cobra.Command{
		Use:   "logs [deployment]",
		Short: "Show a build's log, or the project's runtime logs",
		Long: `Show a deployment's build log (default: the newest deployment's); -f follows
a build that is still running until it ends, and exits 5 if it fails.

--runtime shows what the running project logs instead: requests and
function output, newest last; -f keeps printing new entries.`,
		Example: `  orbits logs
  orbits logs dpl_123 -f
  orbits logs --runtime --since 24h --level error
  orbits logs --runtime -f --source functions`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if runtime {
				if len(args) == 1 {
					return &usageError{fmt.Errorf("--runtime shows the project's logs, not one deployment's"), cmd}
				}
				params := &orbits.ListRuntimeLogsParams{Q: search, Limit: min(max(limit, 1), 500)}
				if since != "" {
					r, err := oneOf("since", since, orbits.RuntimeLogRange1h, orbits.RuntimeLogRange24h, orbits.RuntimeLogRange7d)
					if err != nil {
						return &usageError{err, cmd}
					}
					params.Range = r
				}
				if level != "" {
					l, err := oneOf("level", level, orbits.RuntimeLogLevelInfo, orbits.RuntimeLogLevelWarning, orbits.RuntimeLogLevelError)
					if err != nil {
						return &usageError{err, cmd}
					}
					params.Level = l
				}
				if source != "" {
					s, err := oneOf("source", source, orbits.RuntimeLogSourceRequests, orbits.RuntimeLogSourceFunctions)
					if err != nil {
						return &usageError{err, cmd}
					}
					params.Source = s
				}
				return a.runtimeLogs(cmd, c, team, project, params, follow)
			}
			if since != "" || level != "" || source != "" || search != "" {
				return &usageError{fmt.Errorf("--since, --level, --source and --search are for --runtime"), cmd}
			}
			id, err := a.deploymentRef(cmd, c, team, project, argOr(args))
			if err != nil {
				return err
			}
			print := func(l *orbits.DeploymentLogLine) {
				if a.out.JSON {
					b, _ := json.Marshal(l)
					a.out.Line("%s", b)
					return
				}
				text := output.Clean(l.Text)
				if timestamps {
					text = a.out.Dim(l.At.Local().Format("15:04:05")) + " " + text
				}
				a.out.Line("%s", text)
			}
			if follow {
				var done *orbits.LogStreamDone
				for ev, err := range c.Deployments.StreamLogs(ctx(cmd), team, project, id, nil) {
					if err != nil {
						return err
					}
					switch ev.Type {
					case orbits.LogEventLine:
						print(ev.Line)
					case orbits.LogEventDone:
						done = ev.Done
					}
				}
				if done != nil && (done.State == orbits.DeploymentStateFailed || done.State == orbits.DeploymentStateCanceled) {
					d, err := c.Deployments.Get(ctx(cmd), team, project, id)
					if err != nil {
						return err
					}
					d.State, d.Failure = done.State, done.Failure
					return &buildFailed{d}
				}
				return nil
			}
			after := 0
			for {
				page, err := c.Deployments.ListLogs(ctx(cmd), team, project, id, &orbits.ListDeploymentLogsParams{After: after, Limit: 2000})
				if err != nil {
					return err
				}
				for i := range page.Items {
					print(&page.Items[i])
				}
				if page.Done || len(page.Items) == 0 || page.NextAfter <= after {
					if !page.Done {
						a.out.Note("The build is still running: orbits logs %s -f to follow it.", id)
					}
					return nil
				}
				after = page.NextAfter
			}
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	f.BoolVar(&runtime, "runtime", false, "the running project's logs, not a build's")
	f.BoolVarP(&timestamps, "timestamps", "t", false, "show each build line's time")
	f.StringVar(&since, "since", "", "with --runtime: 1h (default), 24h or 7d")
	f.StringVar(&level, "level", "", "with --runtime: info, warning or error")
	f.StringVar(&source, "source", "", "with --runtime: requests or functions")
	f.StringVar(&search, "search", "", "with --runtime: a path, message, status, method or request id")
	f.IntVar(&limit, "limit", 100, "with --runtime: at most this many entries")
	return cmd
}

func (a *app) runtimeLogs(cmd *cobra.Command, c *orbits.Client, team, project string, params *orbits.ListRuntimeLogsParams, follow bool) error {
	seen := map[string]bool{}
	var newest time.Time
	first := true
	for {
		page, err := c.Observability.ListLogs(ctx(cmd), team, project, params)
		if err != nil {
			return err
		}
		items := slices.Clone(page.Items)
		slices.SortStableFunc(items, func(x, y orbits.RuntimeLog) int { return x.At.Compare(y.At) })
		for _, l := range items {
			if seen[l.ID] || (!first && l.At.Before(newest)) {
				continue
			}
			seen[l.ID] = true
			if l.At.After(newest) {
				newest = l.At
			}
			a.printRuntimeLog(l)
		}
		if first && len(items) == 0 && !follow && !a.out.JSON {
			a.out.Note("No runtime logs in that range.")
		}
		first = false
		if !follow {
			return nil
		}
		select {
		case <-ctx(cmd).Done():
			return nil
		case <-time.After(runtimePoll):
		}
	}
}

func (a *app) printRuntimeLog(l orbits.RuntimeLog) {
	if a.out.JSON {
		b, _ := json.Marshal(l)
		a.out.Line("%s", b)
		return
	}
	var parts []string
	parts = append(parts, a.out.Dim(l.At.Local().Format("Jan 02 15:04:05.000")))
	parts = append(parts, fmt.Sprintf("%-7s", strings.ToUpper(string(l.Level))))
	if l.Method != nil {
		req := str(l.Method) + " " + str(l.Path)
		if l.Status != nil {
			req += fmt.Sprintf(" %d", *l.Status)
		}
		if l.DurationMS != nil {
			req += fmt.Sprintf(" %dms", *l.DurationMS)
		}
		parts = append(parts, output.Clean(req))
	}
	if l.Message != nil {
		parts = append(parts, output.Clean(*l.Message))
	}
	a.out.Line("%s", strings.Join(parts, " "))
}
