package drive

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RcloneRunner defines the interface for executing rclone commands (allows mocking in tests)
type RcloneRunner interface {
	Sync(ctx context.Context, srcPath, remoteTarget string) error
	Copy(ctx context.Context, remoteSrc, dstPath string) error
	FileExists(ctx context.Context, remoteTargetFile string) (bool, error)
	DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error
	UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error
}

// Client implements RcloneRunner using os/exec with retry & exponential backoff.
type Client struct {
	rcloneBinary string
	logFile      string
}

// NewClient creates a new Client instance.
func NewClient(logFile string) *Client {
	rcloneBin, err := exec.LookPath("rclone")
	if err != nil {
		rcloneBin = "rclone"
	}
	return &Client{
		rcloneBinary: rcloneBin,
		logFile:      logFile,
	}
}

func (c *Client) logError(format string, v ...interface{}) {
	msg := fmt.Sprintf("[%s] ", time.Now().Format(time.RFC3339)) + fmt.Sprintf(format, v...) + "\n"
	if c.logFile != "" {
		if err := os.MkdirAll(filepath.Dir(c.logFile), 0755); err == nil {
			f, err := os.OpenFile(c.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				_, _ = f.WriteString(msg)
				_ = f.Close()
			}
		}
	}
	log.Print(msg)
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

// Sync runs `rclone sync <srcPath> <remoteTarget>`
func (c *Client) Sync(ctx context.Context, srcPath, remoteTarget string) error {
	return c.executeWithRetry(ctx, "sync", srcPath, remoteTarget)
}

// Copy runs `rclone copy <remoteSrc> <dstPath>`
func (c *Client) Copy(ctx context.Context, remoteSrc, dstPath string) error {
	return c.executeWithRetry(ctx, "copy", remoteSrc, dstPath)
}

// FileExists checks if a remote file exists using `rclone lsf <remoteTargetFile>`
func (c *Client) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	cmd := exec.CommandContext(ctx, c.rcloneBinary, "lsf", remoteTargetFile)
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
