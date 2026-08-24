package internal

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGetSsmPluginName(t *testing.T) {
	name := GetSsmPluginName()
	if runtime.GOOS == "windows" {
		if name != "session-manager-plugin.exe" {
			t.Errorf("plugin name = %q", name)
		}
	} else if name != "session-manager-plugin" {
		t.Errorf("plugin name = %q", name)
	}
}

func TestGetPluginDirectory(t *testing.T) {
	dir := GetPluginDirectory()
	if !strings.Contains(dir, filepath.Join(".gossm", "plugins")) {
		t.Errorf("plugin directory %q does not end in .gossm/plugins", dir)
	}
}

func TestCalculateHash(t *testing.T) {
	hash, err := calculateHash([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if hash != want {
		t.Errorf("hash = %q, want %q", hash, want)
	}
}

func TestPluginInfoRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin-info.json")

	saved := PluginInfo{
		Version:     "1.2.3",
		InstallDate: time.Now().UTC().Truncate(time.Second),
		Source:      "downloaded",
		Hash:        "abc123",
	}
	if err := savePluginInfo(path, saved); err != nil {
		t.Fatalf("savePluginInfo: %v", err)
	}

	loaded, err := loadPluginInfo(path)
	if err != nil {
		t.Fatalf("loadPluginInfo: %v", err)
	}
	if loaded.Version != saved.Version || loaded.Source != saved.Source || loaded.Hash != saved.Hash {
		t.Errorf("loaded = %+v, want %+v", loaded, saved)
	}
}

func TestLoadPluginInfoErrors(t *testing.T) {
	if _, err := loadPluginInfo(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("expected error for missing file")
	}

	corrupt := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPluginInfo(corrupt); err == nil {
		t.Error("expected error for corrupt file")
	}
}

func TestValidatePlugin(t *testing.T) {
	if err := ValidatePlugin(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for missing plugin")
	}

	if runtime.GOOS != "windows" {
		path := filepath.Join(t.TempDir(), "plugin")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePlugin(path); err != nil {
			t.Fatalf("ValidatePlugin on non-executable file: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0111 == 0 {
			t.Error("ValidatePlugin did not make the plugin executable")
		}
	}
}

// buildZip writes a zip archive with the given name→content entries.
func buildZip(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractFromZipFlat(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "plugin.zip")
	content := []byte("windows-plugin")
	buildZip(t, zipPath, map[string][]byte{"session-manager-plugin.exe": content})

	destDir := t.TempDir()
	destPath, err := extractFromZip(zipPath, destDir)
	if err != nil {
		t.Fatalf("extractFromZip: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted content = %q, want %q", got, content)
	}
}

func TestExtractFromZipNested(t *testing.T) {
	// AWS ships the Windows plugin as a zip that contains package.zip.
	inner := filepath.Join(t.TempDir(), "package.zip")
	content := []byte("nested-plugin")
	buildZip(t, inner, map[string][]byte{"bin/session-manager-plugin.exe": content})
	innerBytes, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}

	outer := filepath.Join(t.TempDir(), "SessionManagerPlugin.zip")
	buildZip(t, outer, map[string][]byte{"package.zip": innerBytes})

	destPath, err := extractFromZip(outer, t.TempDir())
	if err != nil {
		t.Fatalf("extractFromZip: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted content = %q, want %q", got, content)
	}
}

func TestExtractFromZipMissingBinary(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "empty.zip")
	buildZip(t, zipPath, map[string][]byte{"readme.txt": []byte("hi")})

	if _, err := extractFromZip(zipPath, t.TempDir()); err == nil {
		t.Error("expected error when the zip has no plugin binary")
	}
}

func TestGetDownloadInfoForPlatform(t *testing.T) {
	url, extract, err := getDownloadInfoForPlatform("1.2.3.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extract == nil {
		t.Error("extract function is nil")
	}
	if !strings.Contains(url, "1.2.3.0") {
		t.Errorf("url %q does not contain the version", url)
	}
	if !strings.HasPrefix(url, "https://s3.amazonaws.com/session-manager-downloads/plugin/") {
		t.Errorf("url %q has unexpected prefix", url)
	}
}

func TestGetEmbeddedPlugin(t *testing.T) {
	dir := t.TempDir()

	data, err := getEmbeddedPlugin(dir)
	if err != nil {
		t.Fatalf("getEmbeddedPlugin: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded plugin is empty")
	}

	if _, err := os.Stat(filepath.Join(dir, GetSsmPluginName())); err != nil {
		t.Errorf("plugin file not written: %v", err)
	}

	info, err := loadPluginInfo(filepath.Join(dir, pluginInfoFile))
	if err != nil {
		t.Fatalf("plugin info not written: %v", err)
	}
	if info.Source != "embedded" || info.Version != "embedded" {
		t.Errorf("plugin info = %+v", info)
	}
}
