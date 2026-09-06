//go:build windows

package dockerentry

import "fmt"

func defaultExec(argv0 string, argv, envv []string) error {
	return fmt.Errorf("nextsql-entrypoint is the Linux container PID 1")
}
