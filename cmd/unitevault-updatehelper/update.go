// Package main implements the detached helper process
// internal/selfupdate.Apply hands off to on Windows to finish a self-update
// (spec 8.4). It replaces an earlier cmd.exe batch script that shelled out
// to `ping`/`taskkill` for delays and force-kills: those are both
// console-subsystem executables, and even with CREATE_NO_WINDOW set on the
// parent cmd.exe, Windows could still briefly flash a console for them in
// some environments (a real user-reported regression). This program is
// compiled with the same "-H=windowsgui" flag as UniteVault.exe itself, so
// it has no console subsystem at all - there's no window for Windows to
// ever flash, by construction, regardless of environment quirks. It's also
// a plain compiled binary rather than a script, so the script-execution-
// policy restrictions that ruled out PowerShell here originally (see
// selfupdate.Apply's doc comment) don't apply to it either.
//
// The core swap/retry logic below (run, swapExecutables) is deliberately
// OS-agnostic pure file I/O so it can be unit-tested on any platform - only
// killProcess and startDetached (process_windows.go / process_other.go)
// need real Windows behavior, and this program is only ever built for and
// shipped on Windows (see .github/workflows/release.yml).
package main

import (
	"os"
	"time"
)

const (
	defaultMaxAttempts = 10
	defaultRetryDelay  = time.Second
)

// run mirrors the old batch script's sequence exactly: wait briefly for the
// caller to have exited gracefully on its own (it only asks the OS to kill
// it by PID as a safety net for a wedged shutdown - see selfupdate.Apply's
// doc comment), force-kill it if it hasn't, then retry swapping newExePath
// into exePath (backing up the current exe at oldExePath) for a few seconds
// before giving up, since a transient lock (e.g. an antivirus scan on the
// newly-downloaded exe) can persist for a moment even after the old process
// is confirmed gone. Finally starts whichever exe ends up at exePath - the
// new one on success, the restored original on total failure - so the user
// is never left with nothing running.
func run(exePath, newExePath, oldExePath string, pid int, maxAttempts int, delay time.Duration, kill func(int), start func(string)) {
	time.Sleep(delay)
	kill(pid)
	time.Sleep(delay)

	// The backup at oldExePath is deliberately removed only once, here -
	// never inside swapExecutables' retry loop - and again only after a
	// confirmed success below. A previous version of this logic (as a batch
	// script) re-deleted it at the top of every retry iteration, which
	// destroyed the just-created backup one iteration after the EXE->OLDEXE
	// rename succeeded but the still-locked NEWEXE->EXE rename kept failing:
	// by the time all attempts were exhausted, neither EXE nor OLDEXE
	// existed any more and nothing could be relaunched, silently
	// uninstalling the app with no way to recover.
	_ = os.Remove(oldExePath)

	if swapExecutables(exePath, newExePath, oldExePath, maxAttempts, delay) {
		_ = os.Remove(oldExePath)
	} else if exists(oldExePath) {
		_ = os.Rename(oldExePath, exePath)
	}

	if exists(exePath) {
		start(exePath)
	}
}

// swapExecutables attempts, up to maxAttempts times, to move exePath out of
// the way to oldExePath and move newExePath into its place. Both moves are
// re-attempted every iteration - harmless once already done, since the
// exists() guard makes a repeat attempt a no-op - because a lock on either
// path can be transient. Returns true once newExePath no longer exists
// (the swap has succeeded).
func swapExecutables(exePath, newExePath, oldExePath string, maxAttempts int, delay time.Duration) bool {
	for i := 0; i < maxAttempts; i++ {
		if exists(exePath) {
			_ = os.Rename(exePath, oldExePath)
		}
		if exists(newExePath) {
			_ = os.Rename(newExePath, exePath)
		}
		if !exists(newExePath) {
			return true
		}
		time.Sleep(delay)
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
