// Pagedog receives incident notifications over HTTP, puts the incidents on a
// Spooler queue, scales workers to the queue, and passes the workers'
// reports on. The server command does all that; the worker command is what
// the server runs per incident, from this same program.
package main

import "github.com/gobwas/cli"

func main() {
	cli.Main(cli.Commands{
		"server": new(serverCommand),
		"worker": new(workerCommand),
	})
}
