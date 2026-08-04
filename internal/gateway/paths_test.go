package gateway

import (
	"path/filepath"
	"testing"
)

func TestCandidateSocketPathsUseCurrentBundleID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDCAPTAIN_GATEWAY_SOCKET", "")

	paths := CandidateSocketPaths()
	if len(paths) != 2 {
		t.Fatalf("CandidateSocketPaths() returned %d paths, want 2", len(paths))
	}
	wantSandboxed := filepath.Join(
		home,
		"Library", "Containers", "dev.buildcaptain.app", "Data",
		"Library", "Application Support", "BuildCaptain", "AgentGateway", "gateway.sock",
	)
	if paths[0] != wantSandboxed {
		t.Errorf("sandboxed socket path = %q, want %q", paths[0], wantSandboxed)
	}
	if got := appBundleID; got != "dev.buildcaptain.app" {
		t.Errorf("appBundleID = %q, want dev.buildcaptain.app", got)
	}
}

func TestCandidateSocketPathsHonorOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "gateway.sock")
	t.Setenv("BUILDCAPTAIN_GATEWAY_SOCKET", want)

	paths := CandidateSocketPaths()
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("CandidateSocketPaths() = %q, want [%q]", paths, want)
	}
}
