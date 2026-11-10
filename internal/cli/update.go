package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/config"
	"github.com/The-Orbits/orbits-cli/internal/update"
)

const installCmd = "curl -fsSL https://orbits.africa/install | bash"

func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI's version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.out.JSON {
				a.out.Value(map[string]string{"version": Version, "sdk": orbits.Version, "go": runtime.Version(), "platform": update.Platform()})
				return nil
			}
			a.out.Line("orbits %s", Version)
			if !a.out.Quiet {
				a.out.Note("orbits-go %s, %s, %s", orbits.Version, runtime.Version(), update.Platform())
			}
			return nil
		},
	}
}

func newUpdateCmd(a *app) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:     "update [version]",
		Aliases: []string{"upgrade"},
		Short:   "Install the latest version of the CLI",
		Long: `Install the latest version of the CLI, or the version given, in place of
this one, from its GitHub releases. The download is checked against the
release's checksums.txt first. A CLI installed with Homebrew or npm is
upgraded with that instead: brew upgrade orbits, or
npm install -g @useorbits/cli@latest.

The CLI says when a newer version is out, at most once a day. Set
ORBITS_NO_UPDATE_NOTIFIER=1 to turn that off.`,
		Example: `  orbits update
  orbits update --check
  orbits update v0.2.0`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			latest, err := update.Latest(ctx(cmd))
			if err != nil {
				return err
			}
			saveUpdateState(latest)
			target := latest
			if len(args) == 1 {
				target = args[0]
				if !strings.HasPrefix(target, "v") {
					target = "v" + target
				}
			}
			if check {
				if a.out.JSON {
					a.out.Value(map[string]any{"current": Version, "latest": latest, "newer": update.Newer(Version, latest)})
					return nil
				}
				if update.Newer(Version, latest) {
					a.out.Line("%s", latest)
					a.out.Next(fmt.Sprintf("orbits %s is out (you have %s). Install it", latest, Version), upgradeCommand())
				} else {
					a.out.Line("%s", Version)
					a.out.Note("orbits %s is the latest version.", Version)
				}
				return nil
			}
			if len(args) == 0 {
				if !update.IsRelease(Version) {
					return fmt.Errorf("this orbits (%s) is not a release build; to replace it with %s, run: orbits update %s", Version, latest, latest)
				}
				if !update.Newer(Version, latest) {
					a.out.Note("orbits %s is the latest version.", Version)
					return nil
				}
			}
			if target == Version {
				a.out.Note("orbits %s is already installed.", Version)
				return nil
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if exe, err = filepath.EvalSymlinks(exe); err != nil {
				return err
			}
			switch m := update.Manager(exe); m {
			case update.Homebrew:
				return fmt.Errorf("this orbits was installed with Homebrew: run %s", update.UpgradeCommand(m))
			case update.NPM:
				return fmt.Errorf("this orbits was installed from npm: run %s (or your package manager's equivalent)", update.UpgradeCommand(m))
			}
			steps := a.out.Steps()
			steps.Start(fmt.Sprintf("Installing orbits %s", target))
			if err := update.Install(ctx(cmd), target, exe); err != nil {
				steps.Fail("", "")
				if errors.Is(err, update.ErrNotWritable) {
					return fmt.Errorf("%w\nRun it with sudo, or reinstall to your home folder: %s", err, installCmd)
				}
				return err
			}
			steps.Done(fmt.Sprintf("Installed orbits %s", target), exe)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only print the latest version")
	return cmd
}

// upgradeCommand is what upgrades this CLI, by how it was installed.
func upgradeCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return update.UpgradeCommand("")
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return update.UpgradeCommand(update.Manager(exe))
}

func updateStatePath() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "update-check.json")
}

func saveUpdateState(latest string) {
	if p := updateStatePath(); p != "" {
		update.SaveState(p, update.State{CheckedAt: time.Now(), Latest: latest})
	}
}

// startUpdateCheck looks for a newer version while the command runs: from
// the last day's answer if there is one, else from the network. It returns
// nil when the notice is not wanted.
func (a *app) startUpdateCheck(cmd *cobra.Command) <-chan string {
	switch {
	case !update.IsRelease(Version),
		a.out.JSON, a.out.Quiet,
		os.Getenv("ORBITS_NO_UPDATE_NOTIFIER") != "",
		os.Getenv("CI") != "",
		!term.IsTerminal(int(os.Stderr.Fd())):
		return nil
	}
	switch cmd.Name() {
	case "update", "version", "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return nil
	}
	path := updateStatePath()
	if path == "" {
		return nil
	}
	found := make(chan string, 1)
	if s := update.LoadState(path); time.Since(s.CheckedAt) < update.CheckInterval {
		found <- s.Latest
		return found
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		latest, err := update.Latest(c)
		if err == nil {
			update.SaveState(path, update.State{CheckedAt: time.Now(), Latest: latest})
		}
		found <- latest
	}()
	return found
}

// updateNotice prints the notice if a newer version was found. A check on
// the network, once a day, is waited for up to two seconds.
func (a *app) updateNotice(found <-chan string) {
	if found == nil {
		return
	}
	select {
	case latest := <-found:
		if update.Newer(Version, latest) {
			a.out.Note("")
			a.out.Note("orbits %s is out (you have %s).", latest, Version)
			a.out.Next("Update", upgradeCommand())
		}
	case <-time.After(2 * time.Second):
	}
}
