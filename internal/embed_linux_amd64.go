//go:build linux && amd64

package internal

import _ "embed"

// embeddedPlugin is the session-manager-plugin bundled for this build's
// platform, used as a fallback when downloading the plugin fails.
//
//go:embed assets/plugin/linux_amd64/session-manager-plugin
var embeddedPlugin []byte
