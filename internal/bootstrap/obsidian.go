package bootstrap

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// QuitObsidian best-effort asks Obsidian to quit if it's currently
// running, and waits briefly for it to actually exit, so a following
// MoveVaultFolder (Vault Migration, spec 1.6.7) doesn't fail against files
// Obsidian still has open - a hard failure on Windows (file locks); on
// macOS a rename can succeed even so, but leaves Obsidian holding stale
// handles into the now-moved folder until it's restarted.
//
// Deliberately targets Obsidian by name rather than first checking
// whether the specific Vault folder being moved is the one currently open
// in it: Obsidian doesn't expose that in a way this can reliably query
// across both platforms, and in practice it's exactly the Vault a user is
// migrating via this flow anyway.
//
// Never fails or blocks the caller on Obsidian's behavior: if it isn't
// running, has already quit, or doesn't quit within the timeout,
// MoveVaultFolder is left to fail on its own (with a clear error) rather
// than this function reporting a false success or failure of its own.
func QuitObsidian(ctx context.Context) {
	if !obsidianRunning(ctx) {
		return
	}
	requestObsidianQuit(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !obsidianRunning(ctx) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func obsidianRunning(ctx context.Context) bool {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "pgrep", "-x", "Obsidian").Run() == nil
	case "windows":
		cmd := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq Obsidian.exe", "/NH")
		hideWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return false
		}
		return strings.Contains(strings.ToLower(string(out)), "obsidian.exe")
	default:
		return false
	}
}

// requestObsidianQuit sends a normal quit request - the same as the user
// choosing Quit from the app/dock menu (macOS) or clicking its window's
// close button (Windows) - so Obsidian's own save/cleanup logic runs
// normally. Deliberately never force-kills: a Vault Migration that has to
// wait (or, worst case, fail cleanly at the move step) is preferable to
// one that risks losing unsaved state.
func requestObsidianQuit(ctx context.Context) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.CommandContext(ctx, "osascript", "-e", `tell application "Obsidian" to quit`).Run()
	case "windows":
		cmd := exec.CommandContext(ctx, "taskkill", "/IM", "Obsidian.exe")
		hideWindow(cmd)
		_ = cmd.Run()
	}
}
