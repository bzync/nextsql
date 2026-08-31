//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package hosting

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func platformTryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return true, nil
	}
	return false, err
}

func platformUnlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
