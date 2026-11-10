package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newDomainsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domains",
		Aliases: []string{"domain"},
		Short:   "Manage the team's domains and their DNS records",
	}
	cmd.AddCommand(newDomainsListCmd(a), newDomainsAddCmd(a), newDomainsRemoveCmd(a), newDomainsVerifyCmd(a), newRecordsCmd(a))
	return cmd
}

func newDomainsListCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List domains: the linked project's, or the team's with --all",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			params := &orbits.ListDomainsParams{}
			if !all {
				if _, project, err := a.projectRef(cmd, ""); err == nil {
					params.Project = project
				} else if err != errNoProject {
					return err
				}
			}
			list, err := c.Domains.List(ctx(cmd), team, params)
			if err != nil {
				return err
			}
			if printList(a, list.Items, func(d orbits.Domain) string { return d.Name }, "No domains yet.", "Add one", "orbits domains add <domain>") {
				return nil
			}
			rows := make([][]string, len(list.Items))
			for i, d := range list.Items {
				serves := "not in use"
				switch {
				case d.Redirect != nil:
					serves = "→ " + d.Redirect.To
				case d.Project != nil && d.Branch != nil:
					serves = d.Project.Slug + " (" + *d.Branch + ")"
				case d.Project != nil:
					serves = d.Project.Slug
				}
				rows[i] = []string{d.Name, state(a.out, string(d.State)), string(d.DNS), serves, string(d.Certificate.State), output.Ago(d.CreatedAt)}
			}
			a.out.Table([]string{"domain", "state", "dns", "serves", "certificate", "added"}, rows)
			a.out.Summary(count(len(list.Items), "domain"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every domain of the team, not only the linked project's")
	return cmd
}

func newDomainsAddCmd(a *app) *cobra.Command {
	var branch, redirect, dns string
	var none bool
	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Add a domain to the linked project (or --project)",
		Long: `Add a domain. It serves the linked project's production (or --project's), a
branch's newest preview with --branch, or redirects with --redirect-to. With
--no-project it is only added to the team.

Then point its DNS where "orbits domains verify" says, or use Orbits DNS
(--dns orbits) and set the nameservers it names at your registrar.`,
		Example: `  orbits domains add shop.example.com
  orbits domains add staging.example.com --branch staging
  orbits domains add www.example.com --redirect-to example.com
  orbits domains add example.com --dns orbits`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			req := orbits.AddDomainRequest{Domain: args[0]}
			if dns != "" {
				m, err := oneOf("dns", dns, orbits.DNSModeExternal, orbits.DNSModeOrbits)
				if err != nil {
					return &usageError{err, cmd}
				}
				req.DNS = &m
			}
			if redirect != "" {
				req.To = &redirect
			} else if !none {
				_, project, err := a.projectRef(cmd, "")
				if err != nil {
					return err
				}
				req.Project = &project
				if branch != "" {
					req.Branch = &branch
				}
			}
			d, err := c.Domains.Add(ctx(cmd), team, req)
			if err != nil {
				return err
			}
			if printValue(a, d, d.Name) {
				return nil
			}
			a.out.Success("Added %s.", d.Name)
			a.printDomainSetup(d)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&branch, "branch", "", "serve this branch's newest ready preview instead of production")
	f.StringVar(&redirect, "redirect-to", "", "redirect every request to this hostname instead of serving a project")
	f.StringVar(&dns, "dns", "", "external (default: keep your DNS provider) or orbits (Orbits DNS)")
	f.BoolVar(&none, "no-project", false, "add the domain to the team without a project")
	return cmd
}

// printDomainSetup says what DNS a domain needs, if anything.
func (a *app) printDomainSetup(d *orbits.Domain) {
	switch {
	case d.State == orbits.DomainStateValid:
		a.out.Note("Its DNS is set up.")
	case d.RequiredRecord != nil:
		r := d.RequiredRecord
		a.out.Note("Add this record at your DNS provider:\n")
		a.out.Table([]string{"type", "name", "value"}, [][]string{{string(r.Type), r.Name, r.Value}})
		a.out.Next("\nThen check it", "orbits domains verify "+d.Name)
	case d.Nameservers != nil && !d.Nameservers.Pointing:
		a.out.Note("Set these nameservers at your registrar:\n")
		for _, n := range d.Nameservers.Names {
			a.out.Line("  %s", output.Clean(n))
		}
		a.out.Next("\nThen check it", "orbits domains verify "+d.Name)
	}
}

func newDomainsRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <domain>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a domain from the team",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Remove %s? It stops serving at once, and an Orbits DNS zone's records go with it.", args[0])); err != nil {
				return err
			}
			if err := c.Domains.Remove(ctx(cmd), team, args[0]); err != nil {
				return err
			}
			if printValue(a, map[string]string{"removed": args[0]}, args[0]) {
				return nil
			}
			a.out.Success("Removed %s.", args[0])
			return nil
		},
	}
}

func newDomainsVerifyCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "verify <domain>",
		Aliases: []string{"check", "inspect"},
		Short:   "Check a domain's DNS now, and say what it still needs",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			d, err := c.Domains.Check(ctx(cmd), team, args[0])
			if err != nil {
				return err
			}
			if printValue(a, d, d.Name) {
				return nil
			}
			cert := string(d.Certificate.State)
			if d.Certificate.ExpiresAt != nil {
				cert += ", until " + d.Certificate.ExpiresAt.Local().Format("2 Jan 2006")
			}
			a.out.Print(output.Detail{Title: d.Name, State: a.out.State(string(d.State)), Sections: [][]output.Pair{
				{{"DNS", string(d.DNS)}, {"Found", or(str(d.Seen))}, {"Certificate", cert}, {"Checked", output.WhenPtr(d.CheckedAt)}},
			}})
			if d.State != orbits.DomainStateValid {
				a.out.Line("")
			}
			a.printDomainSetup(d)
			return nil
		},
	}
}

func newRecordsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "records",
		Aliases: []string{"dns"},
		Short:   "Manage the DNS records of a domain on Orbits DNS",
	}
	cmd.AddCommand(newRecordsListCmd(a), newRecordsAddCmd(a), newRecordsRemoveCmd(a))
	return cmd
}

func newRecordsListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <domain>",
		Aliases: []string{"list"},
		Short:   "List a zone's records",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			list, err := c.Domains.ListRecords(ctx(cmd), team, args[0])
			if err != nil {
				return err
			}
			if printList(a, list.Items, func(r orbits.DNSRecord) string { return r.ID }, "No records yet.", "Add one", "orbits domains records add "+args[0]+" <type> <name> <value>") {
				return nil
			}
			rows := make([][]string, len(list.Items))
			for i, r := range list.Items {
				ttl := "auto"
				if r.TTL > 0 {
					ttl = strconv.Itoa(r.TTL)
				}
				managed := ""
				if r.Managed {
					managed = a.out.Dim("managed by Orbits")
				}
				rows[i] = []string{a.out.Dim(r.ID), string(r.Type), r.Name, trimTo(r.Value, 60), ttl, managed}
			}
			a.out.Table([]string{"record", "type", "name", "value", "ttl", ""}, rows)
			return nil
		},
	}
}

func newRecordsAddCmd(a *app) *cobra.Command {
	var ttl int
	cmd := &cobra.Command{
		Use:   "add <domain> <type> <name> <value>",
		Short: "Add a record: A, AAAA, CNAME, MX, TXT or CAA",
		Long: `Add a record to a zone on Orbits DNS. The name is relative to the zone (@
for the apex). An MX record's value is its priority and server; a TXT
record's is its text without quotes.`,
		Example: `  orbits domains records add example.com A @ 203.0.113.10
  orbits domains records add example.com MX @ "10 mail.example.com"
  orbits domains records add example.com TXT _verify "token-from-the-provider" --ttl 300`,
		Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			typ, err := oneOf("type", strings.ToUpper(args[1]), orbits.DNSRecordTypeA, orbits.DNSRecordTypeAAAA, orbits.DNSRecordTypeCNAME, orbits.DNSRecordTypeMX, orbits.DNSRecordTypeTXT, orbits.DNSRecordTypeCAA)
			if err != nil {
				return &usageError{err, cmd}
			}
			switch ttl {
			case 0, 60, 300, 3600, 86400:
			default:
				return &usageError{fmt.Errorf("--ttl must be 0 (auto), 60, 300, 3600 or 86400"), cmd}
			}
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			r, err := c.Domains.AddRecord(ctx(cmd), team, args[0], orbits.DNSRecordInput{Type: typ, Name: args[2], Value: args[3], TTL: ttl})
			if err != nil {
				return err
			}
			if printValue(a, r, r.ID) {
				return nil
			}
			a.out.Success("Added %s %s (%s).", r.Type, r.Name, r.ID)
			return nil
		},
	}
	cmd.Flags().IntVar(&ttl, "ttl", 0, "seconds: 0 (auto, 5 minutes), 60, 300, 3600 or 86400")
	return cmd
}

func newRecordsRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <domain> <record id>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a record",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("Remove record %s from %s?", args[1], args[0])); err != nil {
				return err
			}
			if err := c.Domains.DeleteRecord(ctx(cmd), team, args[0], args[1]); err != nil {
				return err
			}
			if printValue(a, map[string]string{"removed": args[1]}, args[1]) {
				return nil
			}
			a.out.Success("Removed record %s.", args[1])
			return nil
		},
	}
}
