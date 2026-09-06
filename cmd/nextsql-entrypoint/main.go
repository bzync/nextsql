// Command nextsql-entrypoint is PID 1 of the NextSQL container image. It is
// not a general-purpose CLI — operators talk to nextsql/nextsqld. See
// docs/docker.md.
package main

import (
	"os"

	"github.com/bzync/nextsql/internal/dockerentry"
)

func main() {
	os.Exit(dockerentry.Main())
}
