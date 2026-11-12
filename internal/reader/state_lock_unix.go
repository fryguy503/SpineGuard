//go:build !windows

package reader

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryStateFileLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func unlockStateFile(file *os.File) { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }
