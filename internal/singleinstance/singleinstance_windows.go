//go:build windows

package singleinstance

import "golang.org/x/sys/windows"

// lockFileName names the mutex (var, not const, so tests can point it at
// a name that can't collide with a real UniteVault instance that happens
// to be running on the same machine while `go test` runs) - matches the
// Unix build's variable name/purpose even though Windows has no lock
// file, just a named kernel object.
var lockFileName = "UniteVaultSingleInstance"

// mutexName uses the "Local\" namespace so the lock is scoped to the
// current login session (matching flock's implicit per-OS-user scope on
// the Unix side) rather than "Global\", which would also block a second
// Windows user on the same machine from running their own instance.
func mutexName() string { return `Local\` + lockFileName }

// TryAcquire creates (or opens) a named Win32 mutex. CreateMutex reports
// ERROR_ALREADY_EXISTS via err when another live process already holds
// this name - the OS itself frees the name the instant that process's
// handle closes, on any exit path including a crash, so there's no lock
// file to ever go stale.
func TryAcquire() (release func(), ok bool, err error) {
	noop := func() {}

	namePtr, err := windows.UTF16PtrFromString(mutexName())
	if err != nil {
		return noop, true, err
	}

	handle, err := windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		if err == windows.ERROR_ALREADY_EXISTS {
			windows.CloseHandle(handle)
			return noop, false, nil
		}
		return noop, true, err
	}

	return func() { windows.CloseHandle(handle) }, true, nil
}
