package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/link"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

func newLinkCmd(a *app) *cobra.Command {
	var create bool
	cmd := &cobra.Command{
		Use:   "link [project]",
		Short: "Tie this folder to a project",
		Long: `Tie this folder to an Orbits project, so commands run in it (or below it) use
that project: orbits deploy, logs, env and the rest. The link is
.orbits/project.json, and .orbits is added to .gitignore.

Without a project, pick one from the team's list (or make a new one). With
--create, make a new upload project named after the argument or the folder.`,
		Example: `  orbits link
  orbits link storefront
  orbits link --create
  orbits --team acme link storefront`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The team comes from --team or the profile, never an old link.
			a.linked, a.linkErr = nil, link.ErrNotLinked
			c, team, err := a.clientAndTeam(cmd)
			if err != nil {
				return err
			}
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			var p *orbits.Project
			switch {
			case create:
				name := filepath.Base(wd)
				if len(args) == 1 {
					name = args[0]
				}
				if p, err = createUploadProject(a, cmd, c, team, name); err != nil {
					return err
				}
			case len(args) == 1:
				if p, err = c.Projects.Get(ctx(cmd), team, args[0]); err != nil {
					return err
				}
			default:
				if p, err = a.pickProject(cmd, c, team, filepath.Base(wd)); err != nil {
					return err
				}
			}
			// The link keeps the team's id (it survives a rename) and slug.
			teamID, teamSlug := team, team
			teams, err := collect(c.Teams.All(ctx(cmd), nil), 0)
			if err != nil {
				return err
			}
			for _, t := range teams {
				if t.ID == team || t.Slug == team {
					teamID, teamSlug = t.ID, t.Slug
				}
			}
			path, err := link.Write(wd, link.Link{TeamID: teamID, TeamSlug: teamSlug, ProjectID: p.ID, ProjectSlug: p.Slug})
			if err != nil {
				return err
			}
			changed, err := link.IgnoreGit(wd)
			if err != nil {
				a.out.Warn("Could not add .orbits to .gitignore: %v", err)
			}
			if printValue(a, p, p.ID) {
				return nil
			}
			a.out.Success("Linked %s to %s (%s).", a.out.Bold(filepath.Base(wd)), a.out.Bold(p.Name), teamSlug+"/"+p.Slug)
			a.out.Note("Saved %s.", path)
			if changed {
				a.out.Note("Added .orbits to .gitignore.")
			}
			a.out.Next("Next", "orbits deploy")
			return nil
		},
	}
	cmd.Flags().BoolVar(&create, "create", false, "make a new upload project and link it")
	return cmd
}

func newUnlinkCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "unlink",
		Short: "Remove this folder's link to a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := a.findLink()
			if errors.Is(err, link.ErrNotLinked) {
				a.out.Note("This folder is not linked to a project.")
				return nil
			}
			if err != nil {
				return err
			}
			if err := link.Remove(l.Root); err != nil {
				return err
			}
			a.out.Success("Unlinked %s from %s.", l.Root, or(l.ProjectSlug))
			return nil
		},
	}
}

// pickProject asks which of the team's projects to link, or makes a new one.
func (a *app) pickProject(cmd *cobra.Command, c *orbits.Client, team, folder string) (*orbits.Project, error) {
	if !a.interactive() {
		return nil, &usageError{errors.New("name the project to link: orbits link <project>, or make one with orbits link --create"), cmd}
	}
	projects, err := collect(c.Projects.All(ctx(cmd), team, &orbits.ListProjectsParams{Limit: 100}), 50)
	if err != nil {
		return nil, err
	}
	if len(projects) > 0 {
		a.out.Note("Projects in %s:\n", team)
		for i, p := range projects {
			fmt.Fprintf(a.out.Err, "  %2d  %s  %s\n", i+1, output.Clean(p.Slug), a.out.Dim(sourceKind(p)))
		}
		fmt.Fprintln(a.out.Err)
	}
	answer, err := a.ask(fmt.Sprintf("Link which project? A number or slug, or \"new\" for a new one [new: %s]:", output.Clean(folder)))
	if err != nil {
		return nil, err
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(projects) {
		return &projects[n-1], nil
	}
	if answer == "" || strings.EqualFold(answer, "new") {
		return createUploadProject(a, cmd, c, team, folder)
	}
	for i := range projects {
		if projects[i].Slug == answer || projects[i].ID == answer || projects[i].Name == answer {
			return &projects[i], nil
		}
	}
	return c.Projects.Get(ctx(cmd), team, answer)
}

func createUploadProject(a *app, cmd *cobra.Command, c *orbits.Client, team, name string) (*orbits.Project, error) {
	kind := orbits.ProjectSourceKindUpload
	p, err := c.Projects.Create(ctx(cmd), team, orbits.ProjectCreate{Name: name, SourceKind: &kind})
	if err != nil {
		return nil, err
	}
	a.out.Success("Made project %s (%s), deployed by uploading a folder.", a.out.Bold(p.Name), p.FreeAddress)
	return p, nil
}

func sourceKind(p orbits.Project) string {
	if p.SourceKind != nil && *p.SourceKind == orbits.ProjectSourceKindUpload {
		return "upload"
	}
	if p.Source != nil && p.Source.Repository != "" {
		return p.Source.Repository
	}
	return "git"
}

func isUpload(p *orbits.Project) bool {
	return p.SourceKind != nil && *p.SourceKind == orbits.ProjectSourceKindUpload
}
