package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

var flagEnvironments = []orbits.FlagEnvironmentName{orbits.FlagEnvironmentNameProduction, orbits.FlagEnvironmentNamePreview, orbits.FlagEnvironmentNameDevelopment}

func newFlagsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "flags",
		Aliases: []string{"flag"},
		Short:   "Manage the project's feature flags",
	}
	rules := &cobra.Command{Use: "rules", Short: "Add and remove a flag's targeting rules"}
	rules.AddCommand(newFlagRuleAddCmd(a), newFlagRuleRemoveCmd(a))
	cmd.AddCommand(newFlagsListCmd(a), newFlagsGetCmd(a), newFlagsCreateCmd(a), newFlagsSetCmd(a), newFlagsRemoveCmd(a), rules)
	return cmd
}

func flagState(e orbits.FlagEnvironment, typ orbits.FlagType) string {
	if typ == orbits.FlagTypeBoolean {
		if !e.On {
			return "off"
		}
		if e.Rollout < 100 {
			return fmt.Sprintf("on %d%%", e.Rollout)
		}
		return "on"
	}
	b, _ := json.Marshal(e.Value)
	return string(b)
}

func newFlagsListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the flags and their state in each environment",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			list, err := c.Flags.List(ctx(cmd), team, project, nil)
			if err != nil {
				return err
			}
			if printList(a, list.Items, func(f orbits.Flag) string { return f.Key }, "No flags yet.", "Make one", "orbits flags create <key>") {
				return nil
			}
			rows := make([][]string, len(list.Items))
			for i, f := range list.Items {
				e := f.Environments
				rows[i] = []string{f.Key, string(f.Type), flagState(e.Production, f.Type), flagState(e.Preview, f.Type), flagState(e.Development, f.Type), output.Ago(f.UpdatedAt)}
			}
			a.out.Table([]string{"flag", "type", "production", "preview", "development", "updated"}, rows)
			return nil
		},
	}
}

func newFlagsGetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "get <key>",
		Aliases: []string{"show"},
		Short:   "Show a flag, its rules and history",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			f, err := c.Flags.Get(ctx(cmd), team, project, args[0])
			if err != nil {
				return err
			}
			if printValue(a, f, f.Key) {
				return nil
			}
			var sections [][]output.Pair
			for _, env := range []struct {
				name string
				e    orbits.FlagEnvironment
			}{{"Production", f.Environments.Production}, {"Preview", f.Environments.Preview}, {"Development", f.Environments.Development}} {
				pairs := []output.Pair{{env.name, flagState(env.e, f.Type)}}
				for _, r := range env.e.Rules {
					serve, _ := json.Marshal(r.Serve)
					pairs = append(pairs, output.Pair{"  rule " + r.ID, fmt.Sprintf("%s %s %s → %s", r.Attribute, r.Operator, strings.Join(r.Values, ","), serve)})
				}
				sections = append(sections, pairs)
			}
			for i, h := range f.History {
				if i == 5 {
					break
				}
				sections = append(sections, []output.Pair{{output.Ago(h.At), h.Text}})
			}
			a.out.Print(output.Detail{Title: f.Key, State: string(f.Type), Subtitle: f.Description, Sections: sections})
			return nil
		},
	}
}

func newFlagsCreateCmd(a *app) *cobra.Command {
	var typ, description string
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Make a flag: boolean (default), string or number",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := oneOf("type", typ, orbits.FlagTypeBoolean, orbits.FlagTypeString, orbits.FlagTypeNumber)
			if err != nil {
				return &usageError{err, cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			req := orbits.CreateFlagRequest{Key: args[0], Type: t}
			if description != "" {
				req.Description = &description
			}
			f, err := c.Flags.Create(ctx(cmd), team, project, req)
			if err != nil {
				return err
			}
			if printValue(a, f, f.Key) {
				return nil
			}
			a.out.Success("Made flag %s.", f.Key)
			a.out.Next("Turn it on", "orbits flags set "+f.Key+" --environment production --on")
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "boolean", "boolean, string or number")
	cmd.Flags().StringVar(&description, "description", "", "what the flag is for")
	return cmd
}

// parseServe reads a value from the command line as JSON (true, 42,
// "text"), else as a string.
func parseServe(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

func newFlagsSetCmd(a *app) *cobra.Command {
	var env, value string
	var on, off bool
	var rollout int
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Change a flag in one environment: on, off, rollout or value",
		Example: `  orbits flags set new-checkout --environment production --on --rollout 25
  orbits flags set new-checkout --environment production --off
  orbits flags set banner-text --environment preview --value "Free shipping"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := oneOf("environment", env, flagEnvironments...)
			if err != nil {
				return &usageError{err, cmd}
			}
			req := orbits.SetFlagEnvironmentRequest{}
			switch {
			case on && off:
				return &usageError{fmt.Errorf("--on or --off, not both"), cmd}
			case on:
				req.On = orbits.Ptr(true)
			case off:
				req.On = orbits.Ptr(false)
			}
			if cmd.Flags().Changed("rollout") {
				if rollout < 0 || rollout > 100 {
					return &usageError{fmt.Errorf("--rollout is a percentage, 0 to 100"), cmd}
				}
				req.Rollout = &rollout
			}
			if cmd.Flags().Changed("value") {
				req.Value = parseServe(value)
			}
			if req.On == nil && req.Rollout == nil && req.Value == nil {
				return &usageError{fmt.Errorf("say what to change: --on, --off, --rollout or --value"), cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if e == orbits.FlagEnvironmentNameProduction {
				if err := a.confirm(fmt.Sprintf("Change %s in production?", args[0])); err != nil {
					return err
				}
			}
			f, err := c.Flags.SetEnvironment(ctx(cmd), team, project, args[0], e, req)
			if err != nil {
				return err
			}
			if printValue(a, f, f.Key) {
				return nil
			}
			var now orbits.FlagEnvironment
			switch e {
			case orbits.FlagEnvironmentNameProduction:
				now = f.Environments.Production
			case orbits.FlagEnvironmentNamePreview:
				now = f.Environments.Preview
			default:
				now = f.Environments.Development
			}
			a.out.Success("%s in %s: %s.", f.Key, e, flagState(now, f.Type))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&env, "environment", "", "production, preview or development (required)")
	f.BoolVar(&on, "on", false, "turn a boolean flag on")
	f.BoolVar(&off, "off", false, "turn a boolean flag off")
	f.IntVar(&rollout, "rollout", 100, "the percentage of visitors who get it")
	f.StringVar(&value, "value", "", "a string or number flag's value")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func newFlagsRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <key>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete a flag and its history",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Delete flag %s? Code that reads it gets its default.", args[0])); err != nil {
				return err
			}
			if err := c.Flags.Delete(ctx(cmd), team, project, args[0]); err != nil {
				return err
			}
			if printValue(a, map[string]string{"deleted": args[0]}, args[0]) {
				return nil
			}
			a.out.Success("Deleted flag %s.", args[0])
			return nil
		},
	}
}

func newFlagRuleAddCmd(a *app) *cobra.Command {
	var env, attribute, operator, values, serve string
	cmd := &cobra.Command{
		Use:     "add <key>",
		Short:   "Serve a value to visitors who match a rule",
		Example: `  orbits flags rules add new-checkout --environment production --attribute email --operator ends_with --values @example.com --serve true`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := oneOf("environment", env, flagEnvironments...)
			if err != nil {
				return &usageError{err, cmd}
			}
			op, err := oneOf("operator", operator, orbits.FlagRuleOperatorEquals, orbits.FlagRuleOperatorNotEquals, orbits.FlagRuleOperatorOneOf, orbits.FlagRuleOperatorEndsWith, orbits.FlagRuleOperatorStartsWith, orbits.FlagRuleOperatorContains)
			if err != nil {
				return &usageError{err, cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			f, err := c.Flags.AddRule(ctx(cmd), team, project, args[0], e, orbits.AddFlagRuleRequest{Attribute: attribute, Operator: op, Values: splitList(values), Serve: parseServe(serve)})
			if err != nil {
				return err
			}
			if printValue(a, f, f.Key) {
				return nil
			}
			a.out.Success("Added a rule to %s in %s.", f.Key, e)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&env, "environment", "", "production, preview or development (required)")
	f.StringVar(&attribute, "attribute", "", "the visitor attribute to match, such as email or country (required)")
	f.StringVar(&operator, "operator", "equals", "equals, not_equals, one_of, ends_with, starts_with or contains")
	f.StringVar(&values, "values", "", "comma-separated values to match (required)")
	f.StringVar(&serve, "serve", "true", "what matching visitors get: true, false, a number or text")
	for _, n := range []string{"environment", "attribute", "values"} {
		_ = cmd.MarkFlagRequired(n)
	}
	return cmd
}

func newFlagRuleRemoveCmd(a *app) *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "rm <key> <rule id>",
		Short: "Remove a rule",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := oneOf("environment", env, flagEnvironments...)
			if err != nil {
				return &usageError{err, cmd}
			}
			c, team, project, err := a.clientAndProject(cmd, "")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Remove rule %s from %s in %s?", args[1], args[0], e)); err != nil {
				return err
			}
			if err := c.Flags.RemoveRule(ctx(cmd), team, project, args[0], e, args[1]); err != nil {
				return err
			}
			if printValue(a, map[string]string{"removed": args[1]}, args[1]) {
				return nil
			}
			a.out.Success("Removed rule %s.", args[1])
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "environment", "", "production, preview or development (required)")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}
