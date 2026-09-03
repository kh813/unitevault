//go:build !windows

package singleinstance

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockFileName is a var (not a const) so tests can point it at a name
// that can't collide with a real UniteVault instance that happens to be
// running on the same machine while `go test` runs.
var lockFileName = "unitevault.lock"

// lockFilePath returns ~/.unitevault/<lockFileName>, the same
// ~/.unitevault base directory drive.GetDefaultRcloneTargetPath already
// uses for this app's other per-user state - reused here as a home for a
// lock file with nothing rclone-specific about it.
func lockFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".unitevault", lockFileName), nil
}

// TryAcquire takes an exclusive, non-blocking flock() on this app's own
// lock file. flock() is tied to the open file descriptor, not to any
// content written into the file, so it's released by the kernel the
// moment this process's descriptor closes for any reason (normal exit,
// panic, SIGKILL) - nothing here ever needs to notice a stale lock left
// over from a dead process.
func TryAcquire() (release func(), ok bool, err error) {
	noop := func() {}

	path, err := lockFilePath()
	if err != nil {
		return noop, true, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return noop, true, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return noop, true, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return noop, false, nil
		}
		return noop, true, err
	}

	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}
