//go:build windows

package bootstrap

import "golang.org/x/sys/windows"

// IsAdministrator reports whether the current Windows user account is a
// member of the built-in Administrators group - i.e. whether UAC elevation
// (a simple Yes/No consent prompt) is even available to them, as opposed to
// a standard user, who would instead be asked for a *different*
// administrator's credentials they typically don't have. This checks group
// membership, not whether this process is currently running elevated -
// UniteVault always runs as a normal, non-elevated tray app even for an
// administrator account, so that distinction doesn't apply here.
func IsAdministrator() bool {
	adminSid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	// A zero Token tells CheckTokenMembership (which this wraps) to use the
	// calling thread's own token, checking the real user regardless of
	// whether this process happens to be running elevated.
	isMember, err := windows.Token(0).IsMember(adminSid)
	if err != nil {
		return false
	}
	return isMember
}
