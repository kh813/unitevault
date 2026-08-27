//go:build windows

package bootstrap

import (
	"os/exec"
	"syscall"
)

// hideWindow suppresses the console-window flash Windows would otherwise
// briefly show for a console-subsystem child process (tasklist, taskkill)
// spawned from this GUI application - the same underlying issue
// internal/selfupdate's own createNoWindow fix addresses for the
// self-update helper.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
