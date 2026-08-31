package winexec_test

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/kh813/unitevault/internal/winexec"
)

// TestHideWindow_SetsSysProcAttrOnlyOnWindows guards that HideWindow's
// effect (or lack of it) matches the current platform - a no-op
// elsewhere, since the console-window-flash concern it addresses is
// Windows-specific.
func TestHideWindow_SetsSysProcAttrOnlyOnWindows(t *testing.T) {
	cmd := exec.Command("true")
	winexec.HideWindow(cmd)

	if runtime.GOOS == "windows" {
		if cmd.SysProcAttr == nil {
			t.Error("expected HideWindow to set SysProcAttr on Windows")
		}
	} else if cmd.SysProcAttr != nil {
		t.Errorf("expected HideWindow to be a no-op outside Windows, got %+v", cmd.SysProcAttr)
	}
}
