//go:build !windows

package bootstrap

// IsAdministrator always reports false outside Windows - the Administrators
// group / UAC elevation concept it checks only applies there (see
// AutoInstallICloud, its only caller).
func IsAdministrator() bool {
	return false
}
