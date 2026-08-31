package main

import (
	"fmt"
	"os"
	"strconv"
)

// Args (positional, passed by selfupdate.Apply - never interpolated into
// any script text, so they can't be misinterpreted regardless of spaces or
// special characters): exePath, newExePath, oldExePath, pid.
func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: unitevault-updatehelper <exePath> <newExePath> <oldExePath> <pid>")
		os.Exit(1)
	}

	pid, err := strconv.Atoi(os.Args[4])
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid pid:", os.Args[4])
		os.Exit(1)
	}

	run(os.Args[1], os.Args[2], os.Args[3], pid, defaultMaxAttempts, defaultRetryDelay, killProcess, startDetached)
}
