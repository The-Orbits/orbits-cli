package main

import (
	"errors"
	"fmt"
	"testing"

	orbits "github.com/The-Orbits/orbits-go"

	"github.com/The-Orbits/orbits-cli/internal/cli"
)

func TestExitCode(t *testing.T) {
	_, notice := cli.NewRoot()
	_ = notice
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"unauthenticated":  {&orbits.Error{Status: 401}, 3},
		"forbidden":        {&orbits.Error{Status: 403, Detail: "This API token is read-only"}, 3},
		"not found":        {fmt.Errorf("reading: %w", &orbits.Error{Status: 404}), 4},
		"build failed":     {&orbits.DeploymentError{Deployment: &orbits.DeploymentDetail{}}, 5},
		"operation failed": {fmt.Errorf("deleting: %w", &orbits.OperationError{Operation: &orbits.Operation{ID: "op_1"}}), 5},
		"conflict":         {&orbits.Error{Status: 409}, 1},
		"validation":       {&orbits.Error{Status: 422}, 1},
		"unknown command":  {errors.New(`unknown command "x" for "orbits"`), 2},
		"anything else":    {errors.New("boom"), 1},
	} {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%s: exitCode = %d, want %d", name, got, c.want)
		}
	}
}
