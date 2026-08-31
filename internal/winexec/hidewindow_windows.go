//go:build windows

package winexec

import (
	"os/exec"
	"syscall"
)

// HideWindow suppresses the console-window flash Windows would otherwise
// briefly show for a console-subsystem child process (git, rclone,
// tasklist, taskkill, winget, cmd, ...) spawned from this GUI application.
// CREATE_NO_WINDOW (0x08000000) is what actually prevents the console
// from ever being created at all - HideWindow alone (SysProcAttr's own
// field) only hides a window after Windows has already created one,
// which isn't fast enough to prevent a brief flash on its own; both are
// set together as belt-and-suspenders.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
