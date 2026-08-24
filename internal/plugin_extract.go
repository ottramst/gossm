package internal

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	defer func() { _ = os.RemoveAll(tempDir) }()

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
	defer func() { _ = file.Close() }()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

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

			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return "", fmt.Errorf("failed to write binary: %w", err)
			}
			if err := out.Close(); err != nil {
				return "", fmt.Errorf("failed to close output file: %w", err)
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
	// Check if rpm2cpio and cpio are available
	rpm2cpioExists, _ := exec.LookPath("rpm2cpio")
	cpioExists, _ := exec.LookPath("cpio")

	if rpm2cpioExists == "" || cpioExists == "" {
		return "", fmt.Errorf("rpm2cpio or cpio not available, cannot extract from RPM")
	}

	// Create a temporary directory to extract files
	tempDir, err := os.MkdirTemp("", "rpm-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

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

// extractFromPkg extracts the plugin binary from a Mac .pkg package
func extractFromPkg(pkgPath, destDir string) (string, error) {
	// Create a temporary directory for extraction
	tempDir, err := os.MkdirTemp("", "ssm-plugin-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	if err := expandPkg(pkgPath, tempDir); err != nil {
		return "", err
	}

	pluginPath, err := findPkgPlugin(tempDir)
	if err != nil {
		return "", err
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

// expandPkg expands a macOS .pkg into tempDir using pkgutil, extracting the
// payload with cpio when only the legacy --expand mode is available
func expandPkg(pkgPath, tempDir string) error {
	// First try --expand-full for newer macOS versions
	cmd := exec.Command("pkgutil", "--expand-full", pkgPath, filepath.Join(tempDir, "expanded"))
	if _, err := cmd.CombinedOutput(); err == nil {
		return nil
	}

	// If --expand-full fails (older macOS), try regular --expand
	cmd = exec.Command("pkgutil", "--expand", pkgPath, filepath.Join(tempDir, "expanded"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to expand pkg: %w, output: %s", err, string(output))
	}

	// For regular expand, we need to extract the payload
	payloadPath := filepath.Join(tempDir, "expanded", "sessionmanagerplugin.pkg", "Payload")
	if _, err := os.Stat(payloadPath); err == nil {
		// Extract the payload using cpio
		cmd = exec.Command("sh", "-c", fmt.Sprintf("cd %s && cat %s | gzip -d | cpio -id", tempDir, payloadPath))
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to extract payload: %w, output: %s", err, string(output))
		}
	}

	return nil
}

// findPkgPlugin locates the extracted plugin binary under tempDir
func findPkgPlugin(tempDir string) (string, error) {
	searchDir := filepath.Join(tempDir, "expanded")

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

	for _, path := range possiblePaths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	// If not found, try to find it recursively
	var pluginPath string
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
	if pluginPath == "" {
		return "", fmt.Errorf("session-manager-plugin not found in package")
	}

	return pluginPath, nil
}

// extractFromZip extracts the plugin binary from a Windows .zip package
func extractFromZip(zipPath, destDir string) (string, error) {
	// Open the zip file
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() { _ = reader.Close() }()

	destPath := filepath.Join(destDir, GetSsmPluginName())

	// AWS ships the Windows plugin as a zip containing package.zip; search
	// the nested zip's entries when present, the outer zip's otherwise
	files := reader.File
	for _, f := range reader.File {
		if f.Name != "package.zip" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open package.zip: %w", err)
		}
		nestedData, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read package.zip: %w", err)
		}
		nestedReader, err := zip.NewReader(bytes.NewReader(nestedData), int64(len(nestedData)))
		if err != nil {
			return "", fmt.Errorf("failed to open nested zip: %w", err)
		}
		files = nestedReader.File
		break
	}

	plugin := findPluginInZip(files)
	if plugin == nil {
		return "", fmt.Errorf("session-manager-plugin.exe not found in zip. Found files: %v", zipFileNames(files))
	}
	if err := extractZipEntry(plugin, destPath); err != nil {
		return "", err
	}

	return destPath, nil
}

// findPluginInZip returns the plugin entry from the given zip files, or nil
func findPluginInZip(files []*zip.File) *zip.File {
	for _, f := range files {
		if f.Name == "session-manager-plugin.exe" ||
			f.Name == "bin/session-manager-plugin.exe" ||
			strings.HasSuffix(f.Name, "/session-manager-plugin.exe") ||
			strings.HasSuffix(f.Name, "\\session-manager-plugin.exe") {
			return f
		}
	}
	return nil
}

// extractZipEntry copies one zip entry to destPath
func extractZipEntry(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("failed to open file in zip: %w", err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return fmt.Errorf("failed to extract file: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to close destination file: %w", err)
	}

	return nil
}

// zipFileNames lists zip entry names, for error messages
func zipFileNames(files []*zip.File) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return names
}
