// Package workspace reads the Git context the gateway uses to match run
// configurations to the caller's repository.
package workspace

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
)

// ReadOrEmpty never fails: outside a usable Git repository it returns a
// context with an empty repository, which the app treats as "match only
// configurations open to any repository". BUILDCAPTAIN_REPO overrides the
// detected repository — useful in fork clones whose origin differs from the
// repository a run configuration names.
func ReadOrEmpty(dir, caller string) *gateway.WorkspaceContext {
	if override := Normalize(os.Getenv("BUILDCAPTAIN_REPO")); override != "" {
		context := &gateway.WorkspaceContext{Repository: override, Caller: caller}
		if branch, err := git(dir, "branch", "--show-current"); err == nil {
			context.Branch = branch
		}
		if commit, err := git(dir, "rev-parse", "HEAD"); err == nil {
			context.Commit = commit
		}
		return context
	}
	if context, err := Read(dir, caller); err == nil {
		return context
	}
	return &gateway.WorkspaceContext{Caller: caller}
}

// Read collects repository, branch, and commit for the given directory.
func Read(dir, caller string) (*gateway.WorkspaceContext, error) {
	repository, err := git(dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return nil, err
	}
	normalized := Normalize(repository)
	if normalized == "" {
		return nil, errors.New("Run this command from a Git repository with an origin remote.")
	}
	context := &gateway.WorkspaceContext{Repository: normalized, Caller: caller}
	if branch, err := git(dir, "branch", "--show-current"); err == nil {
		context.Branch = branch
	}
	if commit, err := git(dir, "rev-parse", "HEAD"); err == nil {
		context.Commit = commit
	}
	return context, nil
}

func git(dir string, args ...string) (string, error) {
	command := exec.Command("/usr/bin/git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", errors.New("The current folder is not a usable Git repository.")
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", errors.New("Git did not return the requested repository information.")
	}
	return value, nil
}

// Normalize ports AgentRepository.normalize from AgentGatewayModels.swift:
// git remote URL -> lowercased "host/path" with ".git" stripped, or "" when
// the value is unusable.
func Normalize(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}

	if strings.HasPrefix(text, "git@") {
		if colon := strings.Index(text, ":"); colon >= 0 {
			text = text[4:colon] + "/" + text[colon+1:]
		}
	} else if parsed, err := url.Parse(text); err == nil && parsed.Hostname() != "" {
		text = parsed.Hostname() + "/" + strings.Trim(parsed.Path, "/")
	}

	text = strings.ToLower(text)
	text = strings.TrimSuffix(text, ".git")
	text = strings.Trim(text, "/")
	if !strings.Contains(text, "/") || strings.Contains(text, "@") {
		return ""
	}
	return text
}
