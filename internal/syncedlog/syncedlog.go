package syncedlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kh813/unitevault/internal/scan"
)

// LogEntry represents a single line in log-<device-uuid>.jsonl schema
type LogEntry struct {
	Device     string          `json:"device"`
	Label      string          `json:"label"`
	Seq        int64           `json:"seq"`
	Path       string          `json:"path"`
	OldPath    string          `json:"old_path,omitempty"`
	NewPath    string          `json:"new_path,omitempty"`
	BaseHash   string          `json:"base_hash"`
	ResultHash string          `json:"result_hash"`
	Diff       string          `json:"diff"`
	Action     scan.ActionType `json:"action"`
	TS         string          `json:"ts"`
}

// LogManager handles reading and writing device log files in Vault/_sync/
type LogManager struct {
	vaultPath string
}

// NewLogManager creates a new LogManager for the given Vault path
func NewLogManager(vaultPath string) *LogManager {
	return &LogManager{vaultPath: vaultPath}
}

// SyncDir returns the path to Vault/_sync/
func (lm *LogManager) SyncDir() string {
	return filepath.Join(lm.vaultPath, "_sync")
}

// DeviceLogPath returns the path to log-<deviceID>.jsonl
func (lm *LogManager) DeviceLogPath(deviceID string) string {
	return filepath.Join(lm.SyncDir(), fmt.Sprintf("log-%s.jsonl", deviceID))
}

// AppendLogEntry appends a new LogEntry to log-<deviceID>.jsonl
func (lm *LogManager) AppendLogEntry(entry LogEntry) error {
	if err := os.MkdirAll(lm.SyncDir(), 0755); err != nil {
		return fmt.Errorf("failed to create _sync dir: %w", err)
	}

	path := lm.DeviceLogPath(entry.Device)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open device log file: %w", err)
	}
	defer f.Close()

	// Normalize diff LF just in case
	entry.Diff = string(scan.NormalizeLF([]byte(entry.Diff)))

	if entry.TS == "" {
		entry.TS = time.Now().Format(time.RFC3339)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write log entry: %w", err)
	}

	return nil
}

// GetNextSeq returns the next sequence number for a given device log file
func (lm *LogManager) GetNextSeq(deviceID string) (int64, error) {
	entries, err := lm.ReadDeviceLog(deviceID)
	if err != nil {
		return 1, nil
	}
	if len(entries) == 0 {
		return 1, nil
	}
	return entries[len(entries)-1].Seq + 1, nil
}

// ReadDeviceLog reads all entries from log-<deviceID>.jsonl
func (lm *LogManager) ReadDeviceLog(deviceID string) ([]LogEntry, error) {
	path := lm.DeviceLogPath(deviceID)
	return ReadLogFile(path)
}

// ReadLogFile reads all entries from a jsonl file path
func ReadLogFile(path string) ([]LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("failed to unmarshal log entry: %w", err)
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	return entries, nil
}

// ReadAllDeviceLogs reads all log-*.jsonl files in Vault/_sync/
func (lm *LogManager) ReadAllDeviceLogs() (map[string][]LogEntry, error) {
	syncDir := lm.SyncDir()
	files, err := os.ReadDir(syncDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string][]LogEntry), nil
		}
		return nil, fmt.Errorf("failed to read _sync dir: %w", err)
	}

	result := make(map[string][]LogEntry)
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.HasPrefix(name, "log-") && strings.HasSuffix(name, ".jsonl") {
			deviceID := strings.TrimPrefix(name, "log-")
			deviceID = strings.TrimSuffix(deviceID, ".jsonl")

			entries, err := ReadLogFile(filepath.Join(syncDir, name))
			if err != nil {
				return nil, err
			}
			result[deviceID] = entries
		}
	}

	return result, nil
}

// LatestEntryByPath map key: path -> map[deviceID] -> Latest LogEntry for that path on that device
func (lm *LogManager) LatestEntryByPath() (map[string]map[string]LogEntry, error) {
	allLogs, err := lm.ReadAllDeviceLogs()
	if err != nil {
		return nil, err
	}

	// path -> deviceID -> LogEntry
	latestMap := make(map[string]map[string]LogEntry)

	for deviceID, entries := range allLogs {
		for _, entry := range entries {
			targetPath := entry.Path
			if entry.Action == scan.ActionRename && entry.NewPath != "" {
				targetPath = entry.NewPath
			}

			if _, ok := latestMap[targetPath]; !ok {
				latestMap[targetPath] = make(map[string]LogEntry)
			}

			// Since logs are append-only and ordered by seq, overwrite to keep latest
			latestMap[targetPath][deviceID] = entry
		}
	}

	return latestMap, nil
}

// CreateLogEntryFromChange converts a scan.FileChange to a LogEntry
func CreateLogEntryFromChange(deviceID, label string, seq int64, change scan.FileChange, diff string) LogEntry {
	entry := LogEntry{
		Device:     deviceID,
		Label:      label,
		Seq:        seq,
		Path:       change.Path,
		BaseHash:   change.BaseHash,
		ResultHash: change.ResultHash,
		Diff:       diff,
		Action:     change.Action,
		TS:         time.Now().Format(time.RFC3339),
	}

	if change.Action == scan.ActionRename {
		entry.OldPath = change.OldPath
		entry.NewPath = change.Path
	}

	return entry
}

// SortEntriesBySeq sorts log entries by sequence number
func SortEntriesBySeq(entries []LogEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Seq < entries[j].Seq
	})
}
