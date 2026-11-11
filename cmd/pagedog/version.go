package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spoolersh/pagedog/internal/stamp"
)

// versionCommand prints the build identity: the release the binary was
// built as, the revision and when.
type versionCommand struct{}

func (c *versionCommand) Summary() string {
	return "Prints the version, the revision and the build time."
}

func (c *versionCommand) Run(context.Context, []string) error {
	_, err := fmt.Fprintf(os.Stdout, "pagedog %s (%s, built %s)\n",
		stamp.Version(), stamp.Revision(), stamp.Built(),
	)
	return err
}
