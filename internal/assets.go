package internal

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/json"
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

// Keep embed directive for fallback if download fails
//
//go:embed assets/*
var assets embed.FS

const (
	// defaultPluginVersion is used when no specific version is requested
	defaultPluginVersion = "latest"

	// downloadTimeout is the maximum time allowed for plugin download
	downloadTimeout = 60 * time.Second

	// pluginInfoFile stores version information about the installed plugin
	pluginInfoFile = "plugin-info.json"

	// latestVersionURL is the URL to query for the latest plugin version
	latestVersionURL = "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/VERSION"
)

// PluginInfo stores metadata about the installed plugin
type PluginInfo struct {
	Version     string    `json:"version"`
	InstallDate time.Time `json:"install_date"`
	Source      string    `json:"source"` // "embedded", "downloaded", "package"
	Hash        string    `json:"hash"`   // SHA256 hash of the plugin binary
}

// GetSsmPluginName returns filename for AWS SSM plugin
func GetSsmPluginName() string {
	if strings.ToLower(runtime.GOOS) == "windows" {
		return "session-manager-plugin.exe"
	}
	return "session-manager-plugin"
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

// GetPluginDirectory returns the directory where plugins are stored
func GetPluginDirectory() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home dir can't be determined
		return ".gossm/plugins"
	}
	return filepath.Join(homeDir, ".gossm", "plugins")
}

// getEmbeddedPlugin extracts the plugin from embedded assets
func getEmbeddedPlugin(pluginDir string) ([]byte, error) {
	pluginKey := fmt.Sprintf("plugin/%s_%s/%s",
		strings.ToLower(runtime.GOOS),
		strings.ToLower(runtime.GOARCH),
		GetSsmPluginName())

	data, err := assets.ReadFile("assets/" + pluginKey)
	if err != nil {
		return nil, fmt.Errorf("failed to extract embedded plugin: %w", err)
	}

	// Write plugin to disk
	pluginPath := filepath.Join(pluginDir, GetSsmPluginName())
	if err := os.WriteFile(pluginPath, data, 0755); err != nil {
		return nil, fmt.Errorf("failed to write plugin file: %w", err)
	}

	// Calculate hash
	hash, _ := calculateHash(data)

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

	return data, nil
}

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plugin download failed with status: %s", resp.Status)
	}

	// Create a temporary file to store the download
	tempFile, err := os.CreateTemp("", "session-manager-plugin-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath) // Clean up temporary file

	// Copy downloaded content to temporary file
	_, err = io.Copy(tempFile, resp.Body)
	tempFile.Close()
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
		url := fmt.Sprintf("https://s3.amazonaws.com/session-manager-downloads/plugin/%s/windows_%s/session-manager-plugin.zip",
			version, awsArch)
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

// extractBinary handles direct binary downloads (no packaging)
func extractBinary(srcPath, destDir string) (string, error) {
	destPath := filepath.Join(destDir, GetSsmPluginName())

	// Copy the file
	input, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("failed to read binary: %w", err)
	}

	if err := os.WriteFile(destPath, input, 0755); err != nil {
		return "", fmt.Errorf("failed to write binary: %w", err)
	}

	return destPath, nil
}

// extractFromDeb extracts the plugin binary from a .deb package
func extractFromDeb(debPath, destDir string) (string, error) {
	// Create a temporary directory to extract files
	tempDir, err := os.MkdirTemp("", "deb-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Use ar to extract the data.tar.gz file
	cmd := exec.Command("ar", "x", debPath, "data.tar.gz")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to extract data.tar.gz from deb: %w", err)
	}

	dataTarPath := filepath.Join(tempDir, "data.tar.gz")

	// Extract data.tar.gz to get the binary
	file, err := os.Open(dataTarPath)
	if err != nil {
		return "", fmt.Errorf("failed to open data.tar.gz: %w", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	// Define the path of the binary in the tar file
	binaryPathInTar := "usr/local/sessionmanagerplugin/bin/session-manager-plugin"
	destPath := filepath.Join(destDir, GetSsmPluginName())

	// Extract only the binary
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar: %w", err)
		}

		if header.Name == binaryPathInTar {
			out, err := os.Create(destPath)
			if err != nil {
				return "", fmt.Errorf("failed to create output file: %w", err)
			}
			defer out.Close()

			if _, err := io.Copy(out, tr); err != nil {
				return "", fmt.Errorf("failed to write binary: %w", err)
			}

			if err := os.Chmod(destPath, 0755); err != nil {
				return "", fmt.Errorf("failed to set executable permissions: %w", err)
			}

			return destPath, nil
		}
	}

	return "", fmt.Errorf("binary not found in deb package")
}

// extractFromRpm extracts the plugin binary from an .rpm package
func extractFromRpm(rpmPath, destDir string) (string, error) {
	// This is a simplified version - we'd normally use rpm2cpio and cpio to extract
	// For now, let's install the RPM if rpm is available

	// Check if rpm2cpio and cpio are available
	rpm2cpioExists, _ := exec.LookPath("rpm2cpio")
	cpioExists, _ := exec.LookPath("cpio")

	if rpm2cpioExists != "" && cpioExists != "" {
		// Create a temporary directory to extract files
		tempDir, err := os.MkdirTemp("", "rpm-extract-*")
		if err != nil {
			return "", fmt.Errorf("failed to create temp directory: %w", err)
		}
		defer os.RemoveAll(tempDir)

		// Use rpm2cpio and cpio to extract files
		cmd1 := exec.Command("rpm2cpio", rpmPath)
		cmd2 := exec.Command("cpio", "-idmv")
		cmd2.Dir = tempDir

		pipe, err := cmd1.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("failed to create pipe: %w", err)
		}
		cmd2.Stdin = pipe

		if err := cmd1.Start(); err != nil {
			return "", fmt.Errorf("failed to start rpm2cpio: %w", err)
		}
		if err := cmd2.Run(); err != nil {
			return "", fmt.Errorf("failed to extract with cpio: %w", err)
		}
		if err := cmd1.Wait(); err != nil {
			return "", fmt.Errorf("rpm2cpio failed: %w", err)
		}

		// Copy the binary to destination
		srcPath := filepath.Join(tempDir, "usr/local/sessionmanagerplugin/bin/session-manager-plugin")
		destPath := filepath.Join(destDir, GetSsmPluginName())

		input, err := os.ReadFile(srcPath)
		if err != nil {
			return "", fmt.Errorf("failed to read extracted binary: %w", err)
		}

		if err := os.WriteFile(destPath, input, 0755); err != nil {
			return "", fmt.Errorf("failed to write binary: %w", err)
		}

		return destPath, nil
	}

	// If rpm2cpio is not available, fall back to embedded plugin
	return "", fmt.Errorf("rpm2cpio or cpio not available, cannot extract from RPM")
}

// extractFromPkg extracts the plugin binary from a Mac .pkg package
func extractFromPkg(pkgPath, destDir string) (string, error) {
	// Create a temporary directory for extraction
	tempDir, err := os.MkdirTemp("", "ssm-plugin-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Use pkgutil to expand the package
	// First try --expand-full for newer macOS versions
	cmd := exec.Command("pkgutil", "--expand-full", pkgPath, filepath.Join(tempDir, "expanded"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If --expand-full fails (older macOS), try regular --expand
		cmd = exec.Command("pkgutil", "--expand", pkgPath, filepath.Join(tempDir, "expanded"))
		output, err = cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("failed to expand pkg: %w, output: %s", err, string(output))
		}
		// For regular expand, we need to extract the payload
		payloadPath := filepath.Join(tempDir, "expanded", "sessionmanagerplugin.pkg", "Payload")
		if _, err := os.Stat(payloadPath); err == nil {
			// Extract the payload using cpio
			cmd = exec.Command("sh", "-c", fmt.Sprintf("cd %s && cat %s | gzip -d | cpio -id", tempDir, payloadPath))
			output, err = cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("failed to extract payload: %w, output: %s", err, string(output))
			}
		}
	}
	
	// Update base directory for searching
	searchDir := filepath.Join(tempDir, "expanded")

	// Look for the plugin binary in the extracted content
	// The binary is typically in Payload/usr/local/sessionmanagerplugin/bin/
	possiblePaths := []string{
		filepath.Join(searchDir, "Payload", "usr", "local", "sessionmanagerplugin", "bin", "session-manager-plugin"),
		filepath.Join(searchDir, "sessionmanagerplugin.pkg", "Payload", "usr", "local", "sessionmanagerplugin", "bin", "session-manager-plugin"),
		// Alternative paths based on different package structures
		filepath.Join(searchDir, "Payload", "usr", "local", "bin", "session-manager-plugin"),
		filepath.Join(searchDir, "sessionmanagerplugin.pkg", "Payload", "usr", "local", "bin", "session-manager-plugin"),
		// Paths for when we extract payload with cpio
		filepath.Join(tempDir, "usr", "local", "sessionmanagerplugin", "bin", "session-manager-plugin"),
		filepath.Join(tempDir, "usr", "local", "bin", "session-manager-plugin"),
	}

	var pluginPath string
	for _, path := range possiblePaths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			pluginPath = path
			break
		}
	}

	if pluginPath == "" {
		// If not found, try to find it recursively
		err := filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if !info.IsDir() && info.Name() == "session-manager-plugin" {
				pluginPath = path
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("failed to search for plugin: %w", err)
		}
	}

	if pluginPath == "" {
		return "", fmt.Errorf("session-manager-plugin not found in package")
	}

	// Copy the plugin to the destination directory
	destPath := filepath.Join(destDir, "session-manager-plugin")
	
	// Read the plugin file
	pluginData, err := os.ReadFile(pluginPath)
	if err != nil {
		return "", fmt.Errorf("failed to read plugin: %w", err)
	}

	// Write to destination with executable permissions
	if err := os.WriteFile(destPath, pluginData, 0755); err != nil {
		return "", fmt.Errorf("failed to write plugin to destination: %w", err)
	}

	return destPath, nil
}

// extractFromZip extracts the plugin binary from a Windows .zip package
// This is simplified and would need expansion for production use
func extractFromZip(zipPath, destDir string) (string, error) {
	// For a complete solution, you'd need to use a zip package like "archive/zip"
	// TODO
	return "", fmt.Errorf("windows .zip extraction not fully implemented")
}

// getLatestVersion fetches the latest available plugin version
func getLatestVersion() (string, error) {
	client := &http.Client{
		Timeout: downloadTimeout,
	}

	resp, err := client.Get(latestVersionURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest version: %w", err)
	}
	defer resp.Body.Close()

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

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
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
