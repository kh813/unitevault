package merge

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncedlog"
)

// ConflictSection represents a section in a file with conflict markers
type ConflictSection struct {
	FilePath string
	Content  string
}

// ConflictResolver provides CLI interactive resolution for conflict markers
type ConflictResolver struct {
	reader io.Reader
	writer io.Writer
}

// NewConflictResolver creates a new ConflictResolver
func NewConflictResolver(r io.Reader, w io.Writer) *ConflictResolver {
	return &ConflictResolver{reader: r, writer: w}
}

// ResolveInteractive prompts the user via CLI to choose options for resolving conflicts
func (cr *ConflictResolver) ResolveInteractive(filePath string, mergedContent string, deviceLabels map[string]string) (string, error) {
	fmt.Fprintf(cr.writer, "\n========================================\n")
	fmt.Fprintf(cr.writer, " [CONFLICT] Conflict detected in: %s\n", filePath)
	fmt.Fprintf(cr.writer, "========================================\n")

	fmt.Fprintf(cr.writer, "Please select an option to resolve:\n")
	options := []string{}
	i := 1
	for devID, label := range deviceLabels {
		opt := fmt.Sprintf("[%d] Use version from device: %s (%s)", i, label, devID)
		options = append(options, opt)
		fmt.Fprintln(cr.writer, opt)
		i++
	}
	fmt.Fprintf(cr.writer, "[%d] Manually edit file in text editor\n", i)
	fmt.Fprintf(cr.writer, "[%d] Keep conflict markers in file for now\n", i+1)

	scanner := bufio.NewScanner(cr.reader)
	fmt.Fprintf(cr.writer, "\nEnter choice (1-%d): ", i+1)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > i+1 {
			return mergedContent, fmt.Errorf("invalid choice: %s", input)
		}

		if choice <= len(deviceLabels) {
			// User picked a device version
			idx := 1
			for devID := range deviceLabels {
				if idx == choice {
					fmt.Fprintf(cr.writer, "Selected device %s version for %s\n", devID, filePath)
					// Caller should supply content for chosen device
					return fmt.Sprintf("SELECTED_DEVICE:%s", devID), nil
				}
				idx++
			}
		} else if choice == len(deviceLabels)+1 {
			fmt.Fprintf(cr.writer, "Please edit %s manually and re-run sync when done.\n", filePath)
			return mergedContent, nil
		} else {
			return mergedContent, nil
		}
	}

	return mergedContent, nil
}

// ResolveAndRecord resolves conflict, writes to vault file, and appends a new LogEntry
func ResolveAndRecord(lm *syncedlog.LogManager, vaultPath, relPath, resolvedContent, deviceID, label string) error {
	fullPath := fmt.Sprintf("%s/%s", vaultPath, relPath)
	if err := os.WriteFile(fullPath, scan.NormalizeLF([]byte(resolvedContent)), 0644); err != nil {
		return fmt.Errorf("failed to write resolved content to file: %w", err)
	}

	hash, err := scan.CalculateNormalizedHash(fullPath)
	if err != nil {
		return fmt.Errorf("failed to hash resolved file: %w", err)
	}

	seq, err := lm.GetNextSeq(deviceID)
	if err != nil {
		return err
	}

	entry := syncedlog.LogEntry{
		Device:     deviceID,
		Label:      label,
		Seq:        seq,
		Path:       relPath,
		BaseHash:   hash,
		ResultHash: hash,
		Diff:       "", // Resolved manually
		Action:     scan.ActionModify,
	}

	return lm.AppendLogEntry(entry)
}
