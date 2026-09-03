package drive

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kh813/unitevault/internal/winexec"
)

// ProgressFunc wraps long-running operations (such as the first-time rclone
// download triggered by NewClient) so a UI layer can surface progress to the
// user. It defaults to a plain console logger so CLI subcommands work without
// any GUI toolkit involved; the GUI layer overrides this once it starts up
// (see gui.RunWithProgress), keeping this package free of any GUI dependency.
var ProgressFunc = func(title, message string, work func() error) error {
	fmt.Printf("%s: %s\n", title, message)
	return work()
}

// RcloneRunner defines the interface for executing rclone commands (allows mocking in tests)
type RcloneRunner interface {
	// Sync and Copy both take optional rclone --exclude patterns
	// (relative to srcPath/remoteSrc), e.g. to keep a device's own
	// private per-device bookkeeping (Scanner.ConfirmedStateFilePath's
	// state/ directory) from ever being pulled from - and so silently
	// overwritten by - another device's copy of it (spec 1.6.4).
	Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error
	Copy(ctx context.Context, remoteSrc, dstPath string, excludes ...string) error
	FileExists(ctx context.Context, remoteTargetFile string) (bool, error)
	DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error
	UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error
	// DeleteFile removes a single remote file. Implementations must treat a
	// file that's already gone as success (idempotent) - callers clearing
	// a marker that may have already been cleared by another device (e.g.
	// PRIMARY_CONFLICT.json once resolved) shouldn't need to check first.
	DeleteFile(ctx context.Context, remoteTargetFile string) error
}

// Client implements RcloneRunner using os/exec with retry & exponential backoff.
type Client struct {
	rcloneBinary string
	logFile      string
}

// GetDefaultRcloneTargetPath returns the path where auto-downloaded rclone binary should be stored (~/.unitevault/bin/rclone or %APPDATA%\unitevault\bin\rclone.exe)
func GetDefaultRcloneTargetPath() (string, error) {
	binName := "rclone"
	if runtime.GOOS == "windows" {
		binName = "rclone.exe"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	var baseDir string
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		baseDir = filepath.Join(appData, "unitevault")
	} else {
		baseDir = filepath.Join(home, ".unitevault")
	}

	return filepath.Join(baseDir, "bin", binName), nil
}

// FindRcloneBinary searches system PATH and user config bin folder for rclone
func FindRcloneBinary() (string, bool) {
	binName := "rclone"
	if runtime.GOOS == "windows" {
		binName = "rclone.exe"
	}

	// 1. Check system PATH
	rcloneBin, err := exec.LookPath(binName)
	if err == nil {
		return rcloneBin, true
	}

	// 2. Check user config bin folder
	targetPath, err := GetDefaultRcloneTargetPath()
	if err == nil {
		if _, err := os.Stat(targetPath); err == nil {
			return targetPath, true
		}
	}

	return "", false
}

// NewClient creates a new Client instance, auto-downloading rclone if not found in PATH or app directory.
func NewClient(logFile string) *Client {
	binPath, found := FindRcloneBinary()
	if found {
		return &Client{rcloneBinary: binPath, logFile: logFile}
	}

	targetPath, err := GetDefaultRcloneTargetPath()
	if err == nil {
		fmt.Printf("rclone binary not found. Downloading rclone for %s/%s...\n", runtime.GOOS, runtime.GOARCH)

		dlErr := ProgressFunc("UniteVault Setup", "Downloading and installing rclone binary...\nPlease wait a moment.", func() error {
			return EnsureRcloneBinary(targetPath)
		})

		if dlErr == nil {
			fmt.Printf("rclone successfully downloaded to: %s\n", targetPath)
			return &Client{rcloneBinary: targetPath, logFile: logFile}
		}
		log.Printf("Failed to auto-download rclone: %v\n", dlErr)
	}

	binName := "rclone"
	if runtime.GOOS == "windows" {
		binName = "rclone.exe"
	}
	return &Client{
		rcloneBinary: binName,
		logFile:      logFile,
	}
}

// EnsureRcloneBinary downloads the appropriate rclone zip archive from official downloads, extracts rclone executable to targetPath.
func EnsureRcloneBinary(targetPath string) error {
	var archiveOS, archiveArch string
	switch runtime.GOOS {
	case "darwin":
		archiveOS = "osx"
	case "windows":
		archiveOS = "windows"
	case "linux":
		archiveOS = "linux"
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	switch runtime.GOARCH {
	case "amd64":
		archiveArch = "amd64"
	case "arm64":
		archiveArch = "arm64"
	default:
		return fmt.Errorf("unsupported Architecture: %s", runtime.GOARCH)
	}

	zipURL := fmt.Sprintf("https://downloads.rclone.org/rclone-current-%s-%s.zip", archiveOS, archiveArch)
	resp, err := http.Get(zipURL)
	if err != nil {
		return fmt.Errorf("http get failed for %s: %w", zipURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download rclone: status %s", resp.Status)
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read zip body: %w", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("failed to parse zip archive: %w", err)
	}

	execName := "rclone"
	if runtime.GOOS == "windows" {
		execName = "rclone.exe"
	}

	for _, file := range zipReader.File {
		if filepath.Base(file.Name) == execName && !file.FileInfo().IsDir() {
			rc, err := file.Open()
			if err != nil {
				return fmt.Errorf("failed to open file inside zip: %w", err)
			}
			defer rc.Close()

			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create directory for binary: %w", err)
			}

			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("failed to create binary file: %w", err)
			}
			defer out.Close()

			if _, err := io.Copy(out, rc); err != nil {
				return fmt.Errorf("failed to write binary content: %w", err)
			}

			return nil
		}
	}

	return fmt.Errorf("rclone executable not found inside zip archive")
}

// maxLogFileSize caps engine.log before it can grow unbounded - a real
// risk during an extended rclone outage, since every failed attempt
// (retried on a 30s/2m/10m backoff, see executeWithRetry) appends another
// entry. Full logrotate-style multi-generation rotation would be overkill
// for a single small diagnostic text file, so rotateLogIfOversized instead
// just drops the oldest half once this is exceeded.
const maxLogFileSize = 5 * 1024 * 1024 // 5 MB

func (c *Client) logError(format string, v ...interface{}) {
	msg := fmt.Sprintf("[%s] ", time.Now().Format(time.RFC3339)) + fmt.Sprintf(format, v...) + "\n"
	if c.logFile != "" {
		if err := os.MkdirAll(filepath.Dir(c.logFile), 0755); err == nil {
			rotateLogIfOversized(c.logFile)
			f, err := os.OpenFile(c.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				_, _ = f.WriteString(msg)
				_ = f.Close()
			}
		}
	}
	log.Print(msg)
}

// rotateLogIfOversized drops the oldest half of logPath's content once it
// exceeds maxLogFileSize, cutting at the next line boundary so the kept
// content never starts mid-entry. Best-effort: any error here just leaves
// the file as-is rather than blocking the log write that triggered it -
// growing a little past the cap on a rare failure is harmless.
func rotateLogIfOversized(logPath string) {
	info, err := os.Stat(logPath)
	if err != nil || info.Size() <= maxLogFileSize {
		return
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	cut := len(data) / 2
	if idx := bytes.IndexByte(data[cut:], '\n'); idx >= 0 {
		cut += idx + 1
	}
	notice := fmt.Sprintf("[%s] --- log truncated: older entries dropped to stay under %d bytes ---\n", time.Now().Format(time.RFC3339), maxLogFileSize)
	_ = os.WriteFile(logPath, append([]byte(notice), data[cut:]...), 0644)
}

// executeWithRetry runs an exec.Command with exponential backoff (30s -> 2m -> 10m).
func (c *Client) executeWithRetry(ctx context.Context, args ...string) error {
	backoffs := []time.Duration{
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
	}

	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		cmd := exec.CommandContext(ctx, c.rcloneBinary, args...)
		winexec.HideWindow(cmd)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err == nil {
			return nil
		}

		errMsg := stderr.String()
		lastErr = fmt.Errorf("rclone command failed (args: %v, exit: %v): %s", args, err, errMsg)
		c.logError("Attempt %d failed: %v", attempt+1, lastErr)

		if attempt < len(backoffs) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}

	return fmt.Errorf("rclone operation failed after retries: %w", lastErr)
}

// Sync runs `rclone sync <srcPath> <remoteTarget>` (plus --exclude per
// excludes, if any).
func (c *Client) Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error {
	return c.executeWithRetry(ctx, withExcludes([]string{"sync", srcPath, remoteTarget}, excludes)...)
}

// Copy runs `rclone copy <remoteSrc> <dstPath>` (plus --exclude per
// excludes, if any).
func (c *Client) Copy(ctx context.Context, remoteSrc, dstPath string, excludes ...string) error {
	return c.executeWithRetry(ctx, withExcludes([]string{"copy", remoteSrc, dstPath}, excludes)...)
}

// withExcludes appends "--exclude <pattern>" for each pattern to args.
func withExcludes(args, excludes []string) []string {
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	return args
}

// FileExists checks if a remote file exists using `rclone lsf <remoteTargetFile>`
func (c *Client) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	cmd := exec.CommandContext(ctx, c.rcloneBinary, "lsf", remoteTargetFile)
	winexec.HideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// If exit code indicates not found or err
		return false, nil
	}

	return len(bytes.TrimSpace(stdout.Bytes())) > 0, nil
}

// DownloadFile downloads a single remote file to local destination using `rclone copyto`
func (c *Client) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	return c.executeWithRetry(ctx, "copyto", remoteSourceFile, localDstFile)
}

// UploadFile uploads a single local file to remote target using `rclone copyto`
func (c *Client) UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error {
	return c.executeWithRetry(ctx, "copyto", localSrcFile, remoteTargetFile)
}

// DeleteFile removes a single remote file using `rclone deletefile`. Checks
// existence first rather than trusting deletefile's own not-found exit
// behavior (which isn't guaranteed stable across rclone versions), so
// deleting an already-gone file is always a safe no-op for callers.
func (c *Client) DeleteFile(ctx context.Context, remoteTargetFile string) error {
	exists, err := c.FileExists(ctx, remoteTargetFile)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return c.executeWithRetry(ctx, "deletefile", remoteTargetFile)
}

// ListRemotes executes `rclone listremotes` and returns configured remote names without trailing colon.
func (c *Client) ListRemotes(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, c.rcloneBinary, "listremotes")
	winexec.HideWindow(cmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	lines := strings.Split(stdout.String(), "\n")
	var remotes []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			trimmed = strings.TrimSuffix(trimmed, ":")
			remotes = append(remotes, trimmed)
		}
	}
	return remotes, nil
}

// IsRemoteConfigured checks if remoteName is present in `rclone listremotes`
func (c *Client) IsRemoteConfigured(ctx context.Context, remoteName string) bool {
	remotes, err := c.ListRemotes(ctx)
	if err != nil {
		return false
	}
	target := strings.TrimSuffix(remoteName, ":")
	for _, r := range remotes {
		if strings.EqualFold(r, target) {
			return true
		}
	}
	return false
}

// CreateGoogleDriveRemote non-interactively creates a Google Drive remote using `rclone config create <remoteName> drive`.
// This triggers the browser OAuth flow automatically without terminal interaction.
func (c *Client) CreateGoogleDriveRemote(ctx context.Context, remoteName string) error {
	target := strings.TrimSuffix(remoteName, ":")
	cmd := exec.CommandContext(ctx, c.rcloneBinary, "config", "create", target, "drive")
	winexec.HideWindow(cmd)
	return cmd.Run()
}

// RemoveRemote deletes a configured rclone remote using `rclone config delete <name>`,
// so the user can redo Google Drive setup from scratch (e.g. with a different account).
func (c *Client) RemoveRemote(ctx context.Context, remoteName string) error {
	target := strings.TrimSuffix(remoteName, ":")
	cmd := exec.CommandContext(ctx, c.rcloneBinary, "config", "delete", target)
	winexec.HideWindow(cmd)
	return cmd.Run()
}

// GetBinaryPath returns the resolved rclone executable path
func (c *Client) GetBinaryPath() string {
	return c.rcloneBinary
}
