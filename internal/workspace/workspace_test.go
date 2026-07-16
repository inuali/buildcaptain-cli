package workspace

import "testing"

// Cases mirror the semantics of AgentRepository.normalize in the app's
// AgentGatewayModels.swift.
func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"git@github.com:Acme/Widgets.git", "github.com/acme/widgets"},
		{"git@github.com:acme/widgets", "github.com/acme/widgets"},
		{"https://github.com/Acme/Widgets.git", "github.com/acme/widgets"},
		{"https://github.com/acme/widgets", "github.com/acme/widgets"},
		{"ssh://git@github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"https://gitlab.example.com/group/sub/project.git", "gitlab.example.com/group/sub/project"},
		{"  https://github.com/acme/widgets.git\n", "github.com/acme/widgets"},
		{"github.com/acme/widgets", "github.com/acme/widgets"},
		{"HTTPS://GITHUB.COM/ACME/WIDGETS.GIT", "github.com/acme/widgets"},
		// Unusable values.
		{"", ""},
		{"   ", ""},
		{"just-a-name", ""},
		{"user@host-without-path", ""},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReadOrEmptyOutsideGitRepository(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_REPO", "")
	context := ReadOrEmpty(t.TempDir(), "test")
	if context.Repository != "" || context.Caller != "test" || context.Branch != "" || context.Commit != "" {
		t.Errorf("expected empty workspace with caller, got %+v", context)
	}
}

func TestReadOrEmptyEnvOverride(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_REPO", "https://github.com/Acme/Widgets.git")
	context := ReadOrEmpty(t.TempDir(), "test")
	if context.Repository != "github.com/acme/widgets" {
		t.Errorf("expected normalized override, got %q", context.Repository)
	}
}

func TestReadOrEmptyIgnoresUnusableOverride(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_REPO", "not-a-repo")
	context := ReadOrEmpty(t.TempDir(), "test")
	if context.Repository != "" {
		t.Errorf("unusable override should fall through to detection, got %q", context.Repository)
	}
}

func TestReadUsesThisRepository(t *testing.T) {
	context, err := Read(".", "test")
	if err != nil {
		t.Skipf("not in a git repository with origin: %v", err)
	}
	if context.Repository == "" || context.Caller != "test" {
		t.Errorf("unexpected context: %+v", context)
	}
}
