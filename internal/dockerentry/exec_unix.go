//go:build unix

package dockerentry

import "syscall"

func defaultExec(argv0 string, argv, envv []string) error {
	return syscall.Exec(argv0, argv, envv)
}
