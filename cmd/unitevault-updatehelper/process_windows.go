//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedProcess mirrors windows.DETACHED_PROCESS so the relaunched app
// survives after this helper exits, without pulling in golang.org/x/sys.
const detachedProcess = 0x00000008

// killProcess force-kills pid (the caller of this helper) - a safety net
// for a wedged shutdown, not the primary shutdown path; by the time this
// runs, the caller has normally already exited on its own (see run's doc
// comment in update.go).
func killProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}

// startDetached relaunches the newly-swapped-in exe, detached from this
// helper so it keeps running once the helper exits. No window-hiding flags
// are needed here beyond DETACHED_PROCESS: exePath is UniteVault.exe
// itself, built with "-H=windowsgui", which has no console subsystem to
// ever flash a window for in the first place.
func startDetached(exePath string) {
	cmd := exec.Command(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	_ = cmd.Start()
}
