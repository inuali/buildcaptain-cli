// Package runs owns the CLI's public run ID: how it is written, how a
// user-supplied one is resolved back to the gateway's operation UUID, and the
// small on-disk list of recent runs that makes git-style prefixes work.
//
// The gateway speaks in bare UUIDs, which are indistinguishable from every
// other UUID in a response. The "run_" prefix makes the one ID a caller has to
// carry self-describing, so passing the wrong string fails with an
// explanation instead of a not-found.
package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Prefix marks a CLI run ID. The remainder is the operation UUID as 32
// lowercase hex characters.
const Prefix = "run_"

// minPrefixLength is the shortest abbreviation resolve accepts; below this
// collisions stop being unlikely enough to be worth the convenience.
const minPrefixLength = 6

// maxEntries bounds the recent-runs file. Abbreviations only need to resolve
// against runs a caller might still be looking at.
const maxEntries = 50

// Format renders a gateway operation UUID as the CLI's public run ID.
func Format(operationUUID string) string {
	return Prefix + strings.ToLower(strings.ReplaceAll(operationUUID, "-", ""))
}

// hexOf reduces any accepted spelling — prefixed or bare, dashed or not, any
// case — to lowercase hex, or returns "" when the value cannot be a run ID.
func hexOf(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	text = strings.TrimPrefix(text, Prefix)
	text = strings.ReplaceAll(text, "-", "")
	if text == "" {
		return ""
	}
	for _, character := range text {
		switch {
		case character >= '0' && character <= '9', character >= 'a' && character <= 'f':
		default:
			return ""
		}
	}
	return text
}

// uuidOf expands 32 hex characters into the canonical uppercase UUID the
// gateway's Swift UUID decoder expects.
func uuidOf(hex string) string {
	upper := strings.ToUpper(hex)
	return upper[0:8] + "-" + upper[8:12] + "-" + upper[12:16] + "-" + upper[16:20] + "-" + upper[20:32]
}

// Resolve turns a user-supplied run ID into the operation UUID to send. An
// empty value means "the most recent run this CLI started". Abbreviations are
// matched against the recent-runs file.
func Resolve(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		latest, ok := Latest()
		if !ok {
			return "", errors.New(
				"No run ID given, and this Mac has no recent run to fall back on. " +
					"Start one with: buildcaptain run <configuration>")
		}
		return latest.ID, nil
	}

	hex := hexOf(text)
	if hex == "" {
		return "", fmt.Errorf(
			"%q is not a run ID. Run IDs look like run_1a2b3c4d… and are printed by "+
				"buildcaptain run. If %q is a configuration key, pass it to buildcaptain run instead.",
			text, text)
	}
	if length := len(hex); length > 32 {
		return "", fmt.Errorf("%q is too long for a run ID (%d hex characters, expected 32).", text, length)
	}
	if len(hex) == 32 {
		return uuidOf(hex), nil
	}
	if len(hex) < minPrefixLength {
		return "", fmt.Errorf(
			"%q is too short to identify a run. Use at least %d characters after run_, "+
				"or the full ID printed by buildcaptain run.", text, minPrefixLength)
	}

	var matches []Entry
	for _, entry := range Recent() {
		if strings.HasPrefix(hexOf(entry.ID), hex) {
			matches = append(matches, entry)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", fmt.Errorf(
			"No recent run starts with %q. Pass the full run ID printed by buildcaptain run.", text)
	default:
		descriptions := make([]string, 0, len(matches))
		for _, match := range matches {
			descriptions = append(descriptions, Format(match.ID)+" ("+match.Configuration+")")
		}
		return "", fmt.Errorf("%q matches more than one recent run: %s.",
			text, strings.Join(descriptions, ", "))
	}
}

// Entry is one remembered run.
type Entry struct {
	ID            string    `json:"id"`
	Configuration string    `json:"configuration"`
	At            time.Time `json:"at"`
}

// Remember records a run so later commands can abbreviate its ID or omit it.
// Bookkeeping must never fail a build, so every error here is swallowed; the
// only cost is that abbreviations stop working.
func Remember(operationUUID, configuration string) {
	path, err := storePath()
	if err != nil {
		return
	}
	entries := Recent()
	kept := make([]Entry, 0, len(entries)+1)
	kept = append(kept, Entry{ID: operationUUID, Configuration: configuration, At: time.Now().UTC()})
	for _, entry := range entries {
		if entry.ID == operationUUID {
			continue
		}
		kept = append(kept, entry)
		if len(kept) == maxEntries {
			break
		}
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	// Write-then-rename so a crash cannot leave a half-written list behind.
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
	}
}

// Recent returns the remembered runs, newest first.
func Recent() []Entry {
	path, err := storePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	valid := entries[:0]
	for _, entry := range entries {
		if len(hexOf(entry.ID)) == 32 {
			valid = append(valid, entry)
		}
	}
	sort.SliceStable(valid, func(a, b int) bool { return valid[a].At.After(valid[b].At) })
	return valid
}

// Latest returns the most recently started run.
func Latest() (Entry, bool) {
	entries := Recent()
	if len(entries) == 0 {
		return Entry{}, false
	}
	return entries[0], true
}

// storePath is the recent-runs file. BUILDCAPTAIN_STATE_DIR overrides the
// location; the CLI is unsandboxed, so it writes beside the app's support
// folder rather than inside the app container.
func storePath() (string, error) {
	if override := os.Getenv("BUILDCAPTAIN_STATE_DIR"); override != "" {
		return filepath.Join(override, "recent-runs.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "BuildCaptain", "cli", "recent-runs.json"), nil
}
