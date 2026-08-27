//go:build !windows

package bootstrap

import "os/exec"

// hideWindow is a no-op outside Windows - the console-window-flash concern
// it addresses (see the windows variant) doesn't apply here.
func hideWindow(cmd *exec.Cmd) {}
