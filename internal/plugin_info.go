package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"
)

// pluginInfoFile stores version information about the installed plugin
const pluginInfoFile = "plugin-info.json"

// PluginInfo stores metadata about the installed plugin
type PluginInfo struct {
	Version     string    `json:"version"`
	InstallDate time.Time `json:"install_date"`
	Source      string    `json:"source"` // "embedded", "downloaded", "package"
	Hash        string    `json:"hash"`   // SHA256 hash of the plugin binary
}

// loadPluginInfo loads plugin metadata from file
func loadPluginInfo(filePath string) (PluginInfo, error) {
	var info PluginInfo

	data, err := os.ReadFile(filePath)
	if err != nil {
		return info, err
	}

	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}

	return info, nil
}

// savePluginInfo saves plugin metadata to file
func savePluginInfo(filePath string, info PluginInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

// calculateHash computes the SHA256 hash of data
func calculateHash(data []byte) (string, error) {
	hash := sha256.New()
	if _, err := hash.Write(data); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ValidatePlugin ensures the plugin is valid and executable
func ValidatePlugin(pluginPath string) error {
	// Check if the plugin exists
	fileInfo, err := os.Stat(pluginPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("plugin not found at %s", pluginPath)
	}
	if err != nil {
		return fmt.Errorf("failed to check plugin: %w", err)
	}

	// Check if it's executable (on Unix systems)
	if runtime.GOOS != "windows" {
		if fileInfo.Mode()&0111 == 0 {
			// Try to make it executable
			if err := os.Chmod(pluginPath, 0755); err != nil {
				return fmt.Errorf("plugin is not executable and failed to set permissions: %w", err)
			}
		}
	}

	return nil
}
