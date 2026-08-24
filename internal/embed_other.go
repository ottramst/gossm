//go:build !(linux && (amd64 || arm64)) && !(darwin && (amd64 || arm64)) && !windows

package internal

// embeddedPlugin is empty on platforms without a bundled
// session-manager-plugin; downloading is the only option there.
var embeddedPlugin []byte
