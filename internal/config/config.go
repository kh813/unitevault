package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

// Config represents the local application configuration (~/.unitevault/config.json)
type Config struct {
	VaultPath        string `json:"vault_path"`
	RcloneRemote     string `json:"rclone_remote"`
	RclonePath       string `json:"rclone_path"`
	IntervalSeconds  int    `json:"interval_seconds"`
}

// ConfigManager handles loading and saving settings in the local config directory
type ConfigManager struct {
	configDir string
}

// NewConfigManager initializes a ConfigManager using default OS config directory paths
func NewConfigManager() (*ConfigManager, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	return &ConfigManager{configDir: dir}, nil
}

// NewConfigManagerWithDir initializes a ConfigManager with a custom directory (useful for testing)
func NewConfigManagerWithDir(dir string) *ConfigManager {
	return &ConfigManager{configDir: dir}
}

// GetConfigDir resolves the local settings directory path:
// Mac/Linux: ~/.unitevault/
// Windows: %APPDATA%\unitevault\
func GetConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home dir: %w", err)
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "unitevault"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}
	return filepath.Join(home, ".unitevault"), nil
}

// EnsureDir ensures that the config directory exists
func (cm *ConfigManager) EnsureDir() error {
	return os.MkdirAll(cm.configDir, 0755)
}

// ConfigPath returns the path to config.json
func (cm *ConfigManager) ConfigPath() string {
	return filepath.Join(cm.configDir, "config.json")
}

// DeviceIDPath returns the path to device_id
func (cm *ConfigManager) DeviceIDPath() string {
	return filepath.Join(cm.configDir, "device_id")
}

// RolePath returns the path to role
func (cm *ConfigManager) RolePath() string {
	return filepath.Join(cm.configDir, "role")
}

// LoadConfig reads config.json if present, or returns default values
func (cm *ConfigManager) LoadConfig() (*Config, error) {
	path := cm.ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				IntervalSeconds: 120,
			}, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the given Config struct to config.json
func (cm *ConfigManager) SaveConfig(cfg *Config) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(cm.ConfigPath(), data, 0644)
}

// GetDeviceID reads the device_id file or generates a new UUID if non-existent
func (cm *ConfigManager) GetDeviceID() (string, error) {
	path := cm.DeviceIDPath()
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read device_id: %w", err)
	}

	// Generate new UUID
	if err := cm.EnsureDir(); err != nil {
		return "", err
	}

	newID := uuid.New().String()
	if err := os.WriteFile(path, []byte(newID), 0644); err != nil {
		return "", fmt.Errorf("failed to write device_id: %w", err)
	}

	return newID, nil
}

// LoadRole reads the cached role ("primary" or "secondary")
func (cm *ConfigManager) LoadRole() (string, error) {
	data, err := os.ReadFile(cm.RolePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read role: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveRole saves the cached role ("primary" or "secondary")
func (cm *ConfigManager) SaveRole(role string) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}
	return os.WriteFile(cm.RolePath(), []byte(role), 0644)
}

// ResetConfig removes config.json and role files to restore initial state
func (cm *ConfigManager) ResetConfig() error {
	_ = os.Remove(cm.ConfigPath())
	_ = os.Remove(cm.RolePath())
	return nil
}
