package output

import (
	"testing"

	"github.com/inuali/buildcaptain-cli/internal/runs"
)

func TestLogsCommandQuotesStageNamesForTheShell(t *testing.T) {
	const id = "1A2B3C4D-5E6F-4A8B-9C0D-1E2F3A4B5C6D"
	cases := map[string]string{
		"":              "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d",
		"Build":         "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d --stage Build",
		"Unit tests":    "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d --stage 'Unit tests'",
		"Bob's stage":   `buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d --stage 'Bob'\''s stage'`,
		"Deploy (prod)": "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d --stage 'Deploy (prod)'",
	}
	for stage, want := range cases {
		if got := LogsCommand(runs.Format(id), stage); got != want {
			t.Errorf("LogsCommand(%q) = %q, want %q", stage, got, want)
		}
	}
}
