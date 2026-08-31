//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// killProcess and startDetached only need real Windows behavior - this
// program is only ever built for and shipped on Windows (see
// selfupdate.Apply and .github/workflows/release.yml) - but keeping the
// package buildable on every OS lets run's actual swap/retry logic
// (update.go) be unit-tested without a Windows machine.
func killProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}

func startDetached(exePath string) {
	cmd := exec.Command(exePath)
	_ = cmd.Start()
}
