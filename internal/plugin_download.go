package internal

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// downloadTimeout is the maximum time allowed for the plugin download;
	// it must cover the full body read of a ~12 MB file on slow connections
	downloadTimeout = 5 * time.Minute

	// versionCheckTimeout bounds the tiny VERSION metadata fetch
	versionCheckTimeout = 30 * time.Second

	// latestVersionURL is the URL to query for the latest plugin version
	latestVersionURL = "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/VERSION"
)

// downloadPlugin downloads and installs the specified plugin version
func downloadPlugin(pluginDir string, version string) error {
	// If "latest" is requested, determine the actual latest version
	actualVersion := version
	if version == "latest" {
		var err error
		actualVersion, err = getLatestVersion()
		if err != nil {
			return fmt.Errorf("failed to determine latest version: %w", err)
		}
		fmt.Printf("Latest version is: %s\n", actualVersion)
	}

	// Determine platform-specific download URL and extraction method
	downloadURL, extractFunc, err := getDownloadInfoForPlatform(actualVersion)
	if err != nil {
		return err
	}

	fmt.Printf("Downloading from: %s\n", downloadURL)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: downloadTimeout,
	}

	// Download the plugin
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("plugin download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plugin download failed with status: %s", resp.Status)
	}

	// Create a temporary file to store the download
	tempFile, err := os.CreateTemp("", "session-manager-plugin-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempFilePath := tempFile.Name()
	defer func() { _ = os.Remove(tempFilePath) }() // Clean up temporary file

	// Copy downloaded content to temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if cerr := tempFile.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("failed to save downloaded file: %w", err)
	}

	// Extract the plugin using the platform-specific method
	pluginBinaryPath, err := extractFunc(tempFilePath, pluginDir)
	if err != nil {
		return fmt.Errorf("failed to extract plugin: %w", err)
	}

	// Read the extracted plugin
	pluginData, err := os.ReadFile(pluginBinaryPath)
	if err != nil {
		return fmt.Errorf("failed to read extracted plugin: %w", err)
	}

	// Calculate hash
	hash, _ := calculateHash(pluginData)

	// Save plugin info
	info := PluginInfo{
		Version:     actualVersion,
		InstallDate: time.Now(),
		Source:      "downloaded",
		Hash:        hash,
	}

	if err := savePluginInfo(filepath.Join(pluginDir, pluginInfoFile), info); err != nil {
		fmt.Printf("Warning: failed to save plugin info: %v\n", err)
	}

	fmt.Printf("Successfully installed AWS Session Manager Plugin version %s\n", actualVersion)
	return nil
}

// getLatestVersion fetches the latest available plugin version
func getLatestVersion() (string, error) {
	client := &http.Client{
		Timeout: versionCheckTimeout,
	}

	resp, err := client.Get(latestVersionURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest version: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version check failed with status: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read version data: %w", err)
	}

	// Clean up the version string (remove whitespace, etc.)
	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", fmt.Errorf("received empty version string")
	}

	return version, nil
}

// getDownloadInfoForPlatform returns the download URL and extraction function for the current platform
func getDownloadInfoForPlatform(version string) (string, func(string, string) (string, error), error) {
	goos := strings.ToLower(runtime.GOOS)
	goarch := strings.ToLower(runtime.GOARCH)

	// Map Go architecture to AWS's naming
	archMapping := map[string]string{
		"amd64": "64bit",
		"386":   "32bit",
		"arm64": "arm64",
	}

	awsArch, ok := archMapping[goarch]
	if !ok {
		return "", nil, fmt.Errorf("unsupported architecture: %s", goarch)
	}

	switch goos {
	case "linux":
		// Check if we're on a system that uses .deb or .rpm
		if isDebianBased() {
			url := fmt.Sprintf("https://s3.amazonaws.com/session-manager-downloads/plugin/%s/ubuntu_%s/session-manager-plugin.deb",
				version, awsArch)
			return url, extractFromDeb, nil
		} else if isRpmBased() {
			url := fmt.Sprintf("https://s3.amazonaws.com/session-manager-downloads/plugin/%s/linux_%s/session-manager-plugin.rpm",
				version, awsArch)
			return url, extractFromRpm, nil
		} else {
			// For other Linux distributions, use the direct binary
			url := fmt.Sprintf("https://s3.amazonaws.com/session-manager-downloads/plugin/%s/linux_%s/session-manager-plugin",
				version, awsArch)
			return url, extractBinary, nil
		}
	case "darwin":
		url := fmt.Sprintf("https://s3.amazonaws.com/session-manager-downloads/plugin/%s/mac_%s/session-manager-plugin.pkg",
			version, awsArch)
		return url, extractFromPkg, nil
	case "windows":
		// Windows uses a different URL pattern - no architecture in path
		url := fmt.Sprintf("https://s3.amazonaws.com/session-manager-downloads/plugin/%s/windows/SessionManagerPlugin.zip",
			version)
		return url, extractFromZip, nil
	default:
		return "", nil, fmt.Errorf("unsupported platform: %s_%s", goos, goarch)
	}
}

// isDebianBased checks if the current Linux distribution is Debian-based
func isDebianBased() bool {
	if _, err := os.Stat("/etc/debian_version"); err == nil {
		return true
	}

	// Check for common Debian-based distributions
	for _, file := range []string{"/etc/lsb-release", "/etc/os-release"} {
		if data, err := os.ReadFile(file); err == nil {
			content := string(data)
			if strings.Contains(content, "Debian") ||
				strings.Contains(content, "Ubuntu") ||
				strings.Contains(content, "LinuxMint") {
				return true
			}
		}
	}

	return false
}

// isRpmBased checks if the current Linux distribution is RPM-based
func isRpmBased() bool {
	// Check for RPM package manager
	if _, err := exec.LookPath("rpm"); err == nil {
		return true
	}

	// Check for common RPM-based distributions
	for _, file := range []string{"/etc/redhat-release", "/etc/os-release"} {
		if data, err := os.ReadFile(file); err == nil {
			content := string(data)
			if strings.Contains(content, "Red Hat") ||
				strings.Contains(content, "CentOS") ||
				strings.Contains(content, "Fedora") ||
				strings.Contains(content, "Amazon Linux") {
				return true
			}
		}
	}

	return false
}
