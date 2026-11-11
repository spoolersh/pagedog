// Pagedog receives incident notifications over HTTP, puts the incidents on a
// Spooler queue, scales workers to the queue, and passes the workers'
// reports on. The server command does all that; the worker command is what
// the server runs per incident, from this same program.
package main

import (
	"context"
	"flag"

	"github.com/gobwas/cli"
)

func main() {
	var version bool
	commands := cli.Commands{
		"server":  new(serverCommand),
		"worker":  new(workerCommand),
		"version": new(versionCommand),
	}
	cli.Main(&cli.Container{
		Command: commands,
		DoDefineFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&version,
				"v", false,
				"print the version and exit",
			)
		},
		DoRun: func(ctx context.Context, args []string) error {
			if version {
				return new(versionCommand).Run(ctx, nil)
			}
			return commands.Run(ctx, args)
		},
	})
}
