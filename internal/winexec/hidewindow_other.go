//go:build !windows

package winexec

import "os/exec"

// HideWindow is a no-op outside Windows - the console-window-flash concern
// it addresses (see the windows variant) doesn't apply here.
func HideWindow(cmd *exec.Cmd) {}
