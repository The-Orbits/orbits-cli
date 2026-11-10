package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newUsageCmd(a *app) *cobra.Command {
	var month string
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show the team's usage this month (or --month), by project",
		Example: `  orbits usage
  orbits usage --month 2026-10`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			u, err := c.Usage.Get(ctx(cmd), team, &orbits.UsageParams{Month: month})
			if err != nil {
				return err
			}
			if printValue(a, u, u.Month) {
				return nil
			}
			t := u.Totals
			logs := func(n string) string {
				if !u.LogsAvailable {
					return "unavailable"
				}
				return n
			}
			a.out.Print(output.Detail{Title: "Usage", State: u.Month, Sections: [][]output.Pair{{
				{"Requests", logs(thousands(t.Requests))},
				{"Bandwidth", logs(output.Bytes(t.BandwidthBytes))},
				{"Function runs", logs(thousands(t.FunctionRuns))},
				{"Build minutes", strconv.FormatFloat(t.BuildMinutes, 'f', 1, 64)},
				{"Deployments", thousands(t.Deployments)},
			}}})
			if len(u.ByProject) > 0 {
				a.out.Line("")
				rows := make([][]string, len(u.ByProject))
				for i, p := range u.ByProject {
					rows[i] = []string{p.Slug, logs(thousands(p.Totals.Requests)), logs(output.Bytes(p.Totals.BandwidthBytes)), strconv.FormatFloat(p.Totals.BuildMinutes, 'f', 1, 64), thousands(p.Totals.Deployments)}
				}
				a.out.Table([]string{"project", "requests", "bandwidth", "build min", "deployments"}, rows)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&month, "month", "", "a past month, YYYY-MM (default: this one)")
	return cmd
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func newActivityCmd(a *app) *cobra.Command {
	var since string
	var limit int
	var all bool
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show what happened in the team (or the linked project)",
		Example: `  orbits activity
  orbits activity --all --since 72h`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			params := &orbits.ListActivityParams{Limit: min(max(limit, 1), 100)}
			if !all {
				if _, project, err := a.projectRef(cmd, ""); err == nil {
					params.Project = project
				} else if err != errNoProject {
					return err
				}
			}
			if since != "" {
				d, err := time.ParseDuration(since)
				if err != nil {
					return &usageError{fmt.Errorf("--since is a duration such as 24h: %v", err), cmd}
				}
				params.Since = time.Now().Add(-d)
			}
			items, err := collect(c.Activity.All(ctx(cmd), team, params), limit)
			if err != nil {
				return err
			}
			if printList(a, items, func(x orbits.Activity) string { return x.ID }, "Nothing in that time.") {
				return nil
			}
			rows := make([][]string, len(items))
			for i, x := range items {
				summary := x.Summary
				if x.Failed {
					summary = x.Summary + " " + a.out.Dim("(failed)")
				}
				rows[i] = []string{a.out.Dim(output.Ago(x.CreatedAt)), x.Actor.Name, summary}
			}
			a.out.Table([]string{"when", "who", "what"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "how far back, such as 24h (default: 7 days; at most 90)")
	cmd.Flags().IntVar(&limit, "limit", 50, "at most this many")
	cmd.Flags().BoolVar(&all, "all", false, "the whole team, not only the linked project")
	return cmd
}
