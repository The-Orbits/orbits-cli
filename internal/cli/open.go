package cli

import (
	"errors"
	"net/url"

	"github.com/spf13/cobra"
)

func newOpenCmd(a *app) *cobra.Command {
	var site, printOnly bool
	cmd := &cobra.Command{
		Use:   "open [project]",
		Short: "Open the project in the web app (or its site, with --site)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, team, ref, err := a.clientAndProject(cmd, argOr(args))
			if err != nil {
				return err
			}
			p, err := c.Projects.Get(ctx(cmd), team, ref)
			if err != nil {
				return err
			}
			target := a.webURL() + "/" + url.PathEscape(a.teamSlugFor(team)) + "/" + url.PathEscape(p.Slug)
			if site {
				target = str(p.ProductionURL)
				if target == "" {
					if p.FreeAddress == "" {
						return errors.New("the project has no address yet: deploy it first")
					}
					target = "https://" + p.FreeAddress
				}
			}
			if printOnly || a.out.JSON || !a.interactive() {
				if a.out.JSON {
					a.out.Value(map[string]string{"url": target})
				} else {
					a.out.Line("%s", target)
				}
				return nil
			}
			if err := openBrowser(target); err != nil {
				a.out.Line("%s", target)
				return nil
			}
			a.out.Note("Opened %s", target)
			return nil
		},
	}
	cmd.Flags().BoolVar(&site, "site", false, "open the production site instead")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the address instead of opening it")
	return cmd
}
