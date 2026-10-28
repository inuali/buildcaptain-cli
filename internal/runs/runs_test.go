package runs

import (
	"strings"
	"testing"
)

const sampleUUID = "1A2B3C4D-5E6F-4A8B-9C0D-1E2F3A4B5C6D"

func TestFormatAndResolveRoundTrip(t *testing.T) {
	formatted := Format(sampleUUID)
	if formatted != "run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d" {
		t.Fatalf("Format() = %q", formatted)
	}
	resolved, err := Resolve(formatted)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != sampleUUID {
		t.Errorf("Resolve(%q) = %q, want %q", formatted, resolved, sampleUUID)
	}
}

// Anything that unambiguously spells the same 32 hex characters must work:
// agents copy IDs out of prose, dashes and case included.
func TestResolveAcceptsEverySpelling(t *testing.T) {
	for _, spelling := range []string{
		"run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d",
		"RUN_1A2B3C4D5E6F4A8B9C0D1E2F3A4B5C6D",
		"1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d",
		sampleUUID,
		strings.ToLower(sampleUUID),
		"  run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d  ",
	} {
		resolved, err := Resolve(spelling)
		if err != nil {
			t.Errorf("Resolve(%q): %v", spelling, err)
			continue
		}
		if resolved != sampleUUID {
			t.Errorf("Resolve(%q) = %q, want %q", spelling, resolved, sampleUUID)
		}
	}
}

func TestResolveRejectsNonRunIDsWithGuidance(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_STATE_DIR", t.TempDir())

	_, err := Resolve("deploy-staging")
	if err == nil {
		t.Fatal("a configuration key should not resolve as a run ID")
	}
	// The whole point of the prefix is that a wrong value explains itself.
	if !strings.Contains(err.Error(), "run_") || !strings.Contains(err.Error(), "buildcaptain run") {
		t.Errorf("error should point at the right command and shape, got: %v", err)
	}

	if _, err := Resolve("run_1a2"); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Errorf("short prefix error = %v", err)
	}
	if _, err := Resolve(sampleUUID + "aa"); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("over-long ID error = %v", err)
	}
}

func TestResolvePrefixAgainstRecentRuns(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_STATE_DIR", t.TempDir())
	Remember(sampleUUID, "deploy-staging")

	resolved, err := Resolve("run_1a2b3c")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != sampleUUID {
		t.Errorf("prefix resolved to %q, want %q", resolved, sampleUUID)
	}

	if _, err := Resolve("run_ffffff"); err == nil || !strings.Contains(err.Error(), "No recent run") {
		t.Errorf("unknown prefix error = %v", err)
	}
}

func TestResolveReportsAmbiguousPrefix(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_STATE_DIR", t.TempDir())
	Remember(sampleUUID, "deploy-staging")
	Remember("1A2B3C4D-0000-4A8B-9C0D-1E2F3A4B5C6D", "deploy-production")

	_, err := Resolve("run_1a2b3c")
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous prefix error = %v", err)
	}
	for _, expected := range []string{"deploy-staging", "deploy-production"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error should list %q, got: %v", expected, err)
		}
	}
}

func TestEmptyResolvesToLatestRun(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_STATE_DIR", t.TempDir())

	if _, err := Resolve(""); err == nil || !strings.Contains(err.Error(), "buildcaptain run") {
		t.Errorf("with no history the error should say how to start a run, got: %v", err)
	}

	Remember(sampleUUID, "deploy-staging")
	newest := "99999999-5E6F-4A8B-9C0D-1E2F3A4B5C6D"
	Remember(newest, "deploy-production")

	resolved, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != newest {
		t.Errorf("Resolve(\"\") = %q, want the newest run %q", resolved, newest)
	}
}

// Re-running the same operation must not accumulate duplicates, or a prefix
// that resolves today becomes "ambiguous" tomorrow.
func TestRememberDeduplicates(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_STATE_DIR", t.TempDir())
	Remember(sampleUUID, "deploy-staging")
	Remember(sampleUUID, "deploy-staging")
	if entries := Recent(); len(entries) != 1 {
		t.Errorf("Recent() has %d entries, want 1", len(entries))
	}
	if _, err := Resolve("run_1a2b3c"); err != nil {
		t.Errorf("prefix should still resolve: %v", err)
	}
}

func TestRememberBoundsTheList(t *testing.T) {
	t.Setenv("BUILDCAPTAIN_STATE_DIR", t.TempDir())
	for index := range maxEntries + 10 {
		Remember(uuidOf(strings.Repeat("0", 28)+padHex(index)), "deploy")
	}
	if entries := Recent(); len(entries) != maxEntries {
		t.Errorf("Recent() has %d entries, want the %d cap", len(entries), maxEntries)
	}
}

func padHex(value int) string {
	const digits = "0123456789abcdef"
	out := []byte("0000")
	for position := 3; position >= 0; position-- {
		out[position] = digits[value&0xF]
		value >>= 4
	}
	return string(out)
}

// A corrupt or missing store must degrade to "no history", never fail a call.
func TestRecentToleratesUnusableStore(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("BUILDCAPTAIN_STATE_DIR", directory)
	if entries := Recent(); entries != nil {
		t.Errorf("missing store should read as empty, got %v", entries)
	}
	Remember(sampleUUID, "deploy-staging")
	if entries := Recent(); len(entries) != 1 {
		t.Fatalf("Recent() = %v", entries)
	}
}

func TestIsURLRecognizesRunURLs(t *testing.T) {
	for value, want := range map[string]bool{
		"https://jenkins.example.com/job/x/1/": true,
		"  HTTP://10.0.0.5/job/x/1/":           true,
		"run_1a2b3c":                           false,
		"jenkins.example.com/job/x/1/":         false,
		"":                                     false,
	} {
		if got := IsURL(value); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestParseURLKeepsTheURLAndRejectsWhatCouldNeverPass(t *testing.T) {
	const good = "https://jenkins.example.com/job/jobfoto-teams/job/main/481/"
	if got, err := ParseURL("  " + good + "\n"); err != nil || got != good {
		t.Errorf("ParseURL(good) = %q, %v", got, err)
	}
	for _, bad := range []string{
		"https://",
		"https:///job/x/1/",
		good + "?x=1",
		good + "?",
		good + "#frag",
		"https://user@jenkins.example.com/job/x/1/",
		"https://jenkins.example.com/job/a b/1/",
		"https://jenkins.example.com/job/a\\b/1/",
		"http:opaque",
	} {
		if _, err := ParseURL(bad); err == nil || !strings.Contains(err.Error(), "is not a Jenkins run URL") {
			t.Errorf("ParseURL(%q) error = %v", bad, err)
		}
	}
}
