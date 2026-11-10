package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newAlertsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "alerts",
		Aliases: []string{"alert"},
		Short:   "Manage the project's alerts: error rate, latency, traffic",
	}
	cmd.AddCommand(newAlertsListCmd(a), newAlertCreateCmd(a), newAlertSwitchCmd(a, true), newAlertSwitchCmd(a, false), newAlertEventsCmd(a), newAlertRemoveCmd(a))
	return cmd
}

func alertRule(al orbits.Alert) string {
	unit := ""
	switch al.Metric {
	case orbits.AlertMetricErrorRate:
		unit = "%"
	case orbits.AlertMetricP75, orbits.AlertMetricP99:
		unit = "ms"
	}
	return fmt.Sprintf("%s %s %s%s over %d min", al.Metric, al.Direction, strconv.FormatFloat(al.Threshold, 'f', -1, 64), unit, al.WindowMinutes)
}

func newAlertsListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the project's alerts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			page, err := c.Alerts.List(ctx(cmd), team, project)
			if err != nil {
				return err
			}
			if printList(a, page.Items, func(al orbits.Alert) string { return al.ID }, "No alerts yet.", "Make one", "orbits alerts create <name> --metric error-rate --threshold 5") {
				return nil
			}
			rows := make([][]string, len(page.Items))
			for i, al := range page.Items {
				st := string(al.State)
				if !al.On {
					st = "off"
				}
				ch := make([]string, len(al.Channels))
				for j, x := range al.Channels {
					ch[j] = string(x)
				}
				rows[i] = []string{a.out.Dim(al.ID), al.Name, state(a.out, st), alertRule(al), strings.Join(ch, ", "), output.Ago(al.Since)}
			}
			a.out.Table([]string{"alert", "name", "state", "rule", "channels", "since"}, rows)
			return nil
		},
	}
}

func newAlertCreateCmd(a *app) *cobra.Command {
	var metric, direction, channels, routes string
	var threshold float64
	var window int
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Make an alert",
		Example: `  orbits alerts create "Errors" --metric error-rate --threshold 5 --window 5
  orbits alerts create "Slow" --metric p99 --threshold 1500 --routes "/api/*"
  orbits alerts create "Quiet" --metric requests --direction below --threshold 10 --window 30`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := oneOf("metric", metric, orbits.AlertMetricErrorRate, orbits.AlertMetricP75, orbits.AlertMetricP99, orbits.AlertMetricRequests)
			if err != nil {
				return &usageError{err, cmd}
			}
			dir, err := oneOf("direction", direction, orbits.AlertDirectionAbove, orbits.AlertDirectionBelow)
			if err != nil {
				return &usageError{err, cmd}
			}
			switch window {
			case 1, 5, 10, 30:
			default:
				return &usageError{fmt.Errorf("--window must be 1, 5, 10 or 30 minutes"), cmd}
			}
			var ch []orbits.AlertChannel
			for _, s := range splitList(channels) {
				x, err := oneOf("channels", s, orbits.AlertChannelEmail, orbits.AlertChannelWebhook)
				if err != nil {
					return &usageError{err, cmd}
				}
				ch = append(ch, x)
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			al, err := c.Alerts.Create(ctx(cmd), team, project, orbits.AlertCreate{Name: args[0], Metric: m, Direction: &dir, Threshold: threshold, WindowMinutes: window, Routes: splitList(routes), Channels: ch})
			if err != nil {
				return err
			}
			if printValue(a, al, al.ID) {
				return nil
			}
			a.out.Success("Made alert %s (%s): %s.", al.Name, al.ID, alertRule(*al))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&metric, "metric", "", "error-rate (%), p75 or p99 (ms), or requests (required)")
	f.StringVar(&direction, "direction", "above", "above, or below (requests only)")
	f.Float64Var(&threshold, "threshold", 0, "the value that fires it (required)")
	f.IntVar(&window, "window", 5, "minutes measured: 1, 5, 10 or 30")
	f.StringVar(&routes, "routes", "", "comma-separated routes (a trailing * matches a prefix; default: all)")
	f.StringVar(&channels, "channels", "email", "comma-separated: email, webhook")
	_ = cmd.MarkFlagRequired("metric")
	_ = cmd.MarkFlagRequired("threshold")
	return cmd
}

func newAlertSwitchCmd(a *app, on bool) *cobra.Command {
	use, short := "disable <alert id>", "Switch an alert off"
	if on {
		use, short = "enable <alert id>", "Switch an alert on"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			al, err := c.Alerts.Update(ctx(cmd), team, project, args[0], orbits.AlertUpdate{On: &on})
			if err != nil {
				return err
			}
			if printValue(a, al, al.ID) {
				return nil
			}
			word := "off"
			if al.On {
				word = "on"
			}
			a.out.Success("%s is %s.", al.Name, word)
			return nil
		},
	}
}

func newAlertEventsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "events <alert id>",
		Short: "List when an alert fired and resolved",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			events, err := collect(c.Alerts.AllEvents(ctx(cmd), team, project, args[0], &orbits.ListAlertEventsParams{Limit: 50}), 50)
			if err != nil {
				return err
			}
			if printList(a, events, func(e orbits.AlertEvent) string { return e.ID }, "It has not fired yet.") {
				return nil
			}
			rows := make([][]string, len(events))
			for i, e := range events {
				val := "—"
				if e.Value != nil {
					val = strconv.FormatFloat(*e.Value, 'f', -1, 64)
				}
				reason := ""
				if e.Reason != nil {
					reason = string(*e.Reason)
				}
				rows[i] = []string{output.When(e.At), string(e.Kind), val, reason}
			}
			a.out.Table([]string{"when", "event", "value", "reason"}, rows)
			return nil
		},
	}
}

func newAlertRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <alert id>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete an alert",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Delete alert %s?", args[0])); err != nil {
				return err
			}
			if err := c.Alerts.Delete(ctx(cmd), team, project, args[0]); err != nil {
				return err
			}
			if printValue(a, map[string]string{"deleted": args[0]}, args[0]) {
				return nil
			}
			a.out.Success("Deleted alert %s.", args[0])
			return nil
		},
	}
}
