package gateway

import (
	"os"
	"path/filepath"
)

const appBundleID = "com.buildcaptain.app"

// CandidateSocketPaths returns the gateway socket locations to try, most
// likely first. The sandboxed (Mac App Store) app binds inside its container;
// unsandboxed development builds use the plain Application Support path.
func CandidateSocketPaths() []string {
	if override := os.Getenv("BUILDCAPTAIN_GATEWAY_SOCKET"); override != "" {
		return []string{override}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	suffix := filepath.Join("Library", "Application Support", "BuildCaptain", "AgentGateway", "gateway.sock")
	return []string{
		filepath.Join(home, "Library", "Containers", appBundleID, "Data", suffix),
		filepath.Join(home, suffix),
	}
}
