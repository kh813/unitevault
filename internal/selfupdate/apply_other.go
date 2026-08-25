//go:build !windows && !darwin

package selfupdate

import (
	"context"
	"fmt"
	"runtime"
)

// Apply is unsupported outside macOS/Windows - there is no distributed
// build for other platforms to self-update to (see PlatformAssetName).
func Apply(_ context.Context, _ string) error {
	return fmt.Errorf("self-update is not supported on %s", runtime.GOOS)
}
