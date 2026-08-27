package bootstrap

import (
	"os/exec"
	"runtime"
	"testing"
)

// TestHideWindow_SetsSysProcAttrOnlyOnWindows guards that hideWindow's
// effect (or lack of it) matches the current platform - a no-op elsewhere,
// since the console-window-flash concern it addresses is Windows-specific.
func TestHideWindow_SetsSysProcAttrOnlyOnWindows(t *testing.T) {
	cmd := exec.Command("true")
	hideWindow(cmd)

	if runtime.GOOS == "windows" {
		if cmd.SysProcAttr == nil {
			t.Error("expected hideWindow to set SysProcAttr on Windows")
		}
	} else if cmd.SysProcAttr != nil {
		t.Errorf("expected hideWindow to be a no-op outside Windows, got %+v", cmd.SysProcAttr)
	}
}
