// Package singleinstance stops a second UniteVault tray/GUI process from
// running alongside one that's already up - a real device report: nothing
// stops a user from launching UniteVault.app (macOS) or UniteVault.exe
// (Windows) a second time (double-clicking it again, an OS "run at login"
// entry firing while a manual launch is already open, ...), and two
// SyncEngines racing on the same Vault at once is exactly the kind of
// corruption spec 1.6.4's Primary/Secondary election exists to avoid
// between devices - it was never meant to guard against the same device
// running the app twice.
//
// TryAcquire is implemented per-OS (singleinstance_windows.go /
// singleinstance_unix.go) using a lock primitive the OS itself releases
// automatically on process exit - including a crash - so there is no lock
// file/handle to ever go stale and need manual cleanup.
package singleinstance
