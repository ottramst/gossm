package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultPluginVersion is used when no specific version is requested
const defaultPluginVersion = "latest"

// GetSsmPluginName returns filename for AWS SSM plugin
func GetSsmPluginName() string {
	if strings.ToLower(runtime.GOOS) == "windows" {
		return "session-manager-plugin.exe"
	}
	return "session-manager-plugin"
}

// GetPluginDirectory returns the directory where plugins are stored
func GetPluginDirectory() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home dir can't be determined
		return ".gossm/plugins"
	}
	return filepath.Join(homeDir, ".gossm", "plugins")
}

// GetSsmPlugin retrieves the AWS SSM plugin, downloading it if needed
func GetSsmPlugin() ([]byte, error) {
	// First, try to load already installed plugin
	pluginDir := GetPluginDirectory()
	pluginPath := filepath.Join(pluginDir, GetSsmPluginName())

	// Create plugin directory if it doesn't exist
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// Check if plugin info exists and load it
	infoFilePath := filepath.Join(pluginDir, pluginInfoFile)
	info, infoErr := loadPluginInfo(infoFilePath)

	// Get user-defined version from environment
	requestedVersion := os.Getenv("GOSSM_PLUGIN_VERSION")
	if requestedVersion == "" {
		requestedVersion = defaultPluginVersion
	}

	// Determine if we need to download a new version
	needsDownload := false

	// If info doesn't exist or has different version than requested
	if infoErr != nil || (requestedVersion != "latest" && requestedVersion != info.Version) {
		needsDownload = true
	} else {
		// Check if plugin file exists and is executable
		if err := ValidatePlugin(pluginPath); err != nil {
			needsDownload = true
		}
	}

	// Download new plugin if needed
	if needsDownload {
		fmt.Println("Downloading AWS Session Manager plugin...")
		if err := downloadPlugin(pluginDir, requestedVersion); err != nil {
			// If download fails, fallback to embedded plugin
			fmt.Printf("Download failed, using embedded plugin: %v\n", err)
			return getEmbeddedPlugin(pluginDir)
		}
	}

	// Read the plugin file
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		// If reading fails, fallback to embedded plugin
		fmt.Printf("Failed to read plugin, using embedded plugin: %v\n", err)
		return getEmbeddedPlugin(pluginDir)
	}

	return data, nil
}

// getEmbeddedPlugin installs the plugin bundled into this build. Only the
// current platform's plugin is embedded — see the embed_*.go files.
func getEmbeddedPlugin(pluginDir string) ([]byte, error) {
	if len(embeddedPlugin) == 0 {
		return nil, fmt.Errorf("no embedded session-manager-plugin for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Write plugin to disk
	pluginPath := filepath.Join(pluginDir, GetSsmPluginName())
	if err := os.WriteFile(pluginPath, embeddedPlugin, 0755); err != nil {
		return nil, fmt.Errorf("failed to write plugin file: %w", err)
	}

	// Calculate hash
	hash, _ := calculateHash(embeddedPlugin)

	// Save plugin info
	info := PluginInfo{
		Version:     "embedded",
		InstallDate: time.Now(),
		Source:      "embedded",
		Hash:        hash,
	}
	if err := savePluginInfo(filepath.Join(pluginDir, pluginInfoFile), info); err != nil {
		return nil, err
	}

	return embeddedPlugin, nil
}
