// Command orbits deploys and manages Orbits projects from the terminal.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/cli"
	"github.com/The-Orbits/orbits-cli/internal/output"
)

// Exit codes, for scripts:
//
//	0   it worked
//	1   anything else
//	2   bad usage
//	3   not signed in, or the token was refused (401) or may not do this (403)
//	4   not found (404)
//	5   a build or operation finished, but failed (or was canceled)
//	130 interrupted
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root, notice := cli.NewRoot()
	err := root.ExecuteContext(ctx)
	if err == nil {
		notice()
		return
	}
	if errors.Is(err, context.Canceled) {
		os.Exit(130)
	}
	output.New(false, false).Error(err)
	notice()
	os.Exit(exitCode(err))
}

func exitCode(err error) int {
	switch {
	case cli.IsNotSignedIn(err), orbits.IsUnauthorized(err), orbits.IsForbidden(err):
		return 3
	case orbits.IsNotFound(err):
		return 4
	case cli.IsBuildFailed(err):
		return 5
	case cli.IsUsage(err):
		return 2
	}
	return 1
}
