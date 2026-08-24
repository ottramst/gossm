//go:build windows

package internal

import _ "embed"

// embeddedPlugin is the session-manager-plugin bundled for this build's
// platform, used as a fallback when downloading the plugin fails.
// AWS ships only an amd64 plugin for Windows; arm64 runs it via emulation.
//
//go:embed assets/plugin/windows_amd64/session-manager-plugin.exe
var embeddedPlugin []byte
