package gateway

import (
	"encoding/json"
	"testing"
)

// Older app builds send seconds since 2001-01-01, newer ones ISO 8601. One
// CLI has to read both, and must not silently read the old form as a Unix
// timestamp — that lands in 1994.
func TestTimeAcceptsBothWireForms(t *testing.T) {
	const want = "2026-07-27T12:00:00Z"
	// 806846400 is that instant in seconds since 2001-01-01; read as a Unix
	// timestamp it would be 1995-07-27.
	for _, payload := range []string{`"2026-07-27T12:00:00Z"`, `806846400`} {
		var value Time
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		if got := value.ISO8601(); got != want {
			t.Errorf("%s decoded to %s, want %s", payload, got, want)
		}
	}
}

func TestTimeTreatsMissingAsUnset(t *testing.T) {
	var value Time
	if err := json.Unmarshal([]byte("null"), &value); err != nil {
		t.Fatal(err)
	}
	if value.ISO8601() != "" {
		t.Errorf("null decoded to %q, want empty", value.ISO8601())
	}
}

func TestOperationDecodesAppPayload(t *testing.T) {
	// The fields the app sends but the CLI deliberately drops are present
	// here: decoding must ignore them rather than choke on them.
	payload := `{
	  "id": "1A2B3C4D-5E6F-4A8B-9C0D-1E2F3A4B5C6D",
	  "configurationID": "99999999-5E6F-4A8B-9C0D-1E2F3A4B5C6D",
	  "configurationKey": "deploy-staging",
	  "configurationName": "Deploy staging",
	  "configurationRevision": 806889600,
	  "repository": "github.com/acme/widgets",
	  "branch": "main",
	  "caller": "MCP agent",
	  "inputs": {"TARGET": "staging"},
	  "resolvedParameters": {"TARGET": "staging"},
	  "requestedAt": "2026-07-27T12:00:00Z",
	  "updatedAt": "2026-07-27T12:04:00Z",
	  "state": "completed",
	  "result": "success",
	  "queueID": 4821,
	  "queueURL": "https://ci.example.com/queue/item/4821/",
	  "runID": {"job": {"url": "https://ci.example.com/job/deploy/"}, "number": 7, "url": "https://ci.example.com/job/deploy/7/"},
	  "resolvedJob": {"id": {"url": "https://ci.example.com/job/deploy/"}, "name": "main", "fullDisplayName": "deploy » main", "kind": "pipeline"}
	}`
	var operation Operation
	if err := json.Unmarshal([]byte(payload), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.ConfigurationKey != "deploy-staging" || operation.Result != "success" {
		t.Errorf("unexpected operation: %+v", operation)
	}
	if operation.RunID == nil || operation.RunID.Number != 7 {
		t.Errorf("build number not decoded: %+v", operation.RunID)
	}
	if operation.ResolvedJob == nil || operation.ResolvedJob.FullDisplayName != "deploy » main" {
		t.Errorf("job not decoded: %+v", operation.ResolvedJob)
	}
	if operation.RequestedAt.ISO8601() != "2026-07-27T12:00:00Z" {
		t.Errorf("requestedAt = %q", operation.RequestedAt.ISO8601())
	}
}

func TestOperationLifecycleClassification(t *testing.T) {
	cases := []struct {
		state, result string
		active        bool
		succeeded     bool
	}{
		{StateAwaitingApproval, "", true, false},
		{StateSubmitting, "", true, false},
		{StateQueued, "", true, false},
		{StateRunning, "", true, false},
		{StateCompleted, "success", false, true},
		{StateCompleted, "", false, true},
		{StateCompleted, "failure", false, false},
		{StateCompleted, "unstable", false, false},
		{StateCompleted, "aborted", false, false},
		{StateRejected, "", false, false},
		{StateFailed, "", false, false},
	}
	for _, testCase := range cases {
		operation := Operation{State: testCase.state, Result: testCase.result}
		if operation.IsActive() != testCase.active {
			t.Errorf("%s/%s IsActive() = %v", testCase.state, testCase.result, operation.IsActive())
		}
		if operation.Succeeded() != testCase.succeeded {
			t.Errorf("%s/%s Succeeded() = %v", testCase.state, testCase.result, operation.Succeeded())
		}
	}
}

func TestStateDescriptionReadsAsWords(t *testing.T) {
	cases := []struct {
		operation Operation
		want      string
	}{
		{Operation{State: StateAwaitingApproval}, "awaiting approval"},
		{Operation{State: StateRunning}, "running"},
		{Operation{State: StateCompleted, Result: "success"}, "completed (success)"},
		{Operation{State: StateCompleted, Result: "not_built"}, "completed (not built)"},
	}
	for _, testCase := range cases {
		if got := testCase.operation.StateDescription(); got != testCase.want {
			t.Errorf("StateDescription(%s/%s) = %q, want %q",
				testCase.operation.State, testCase.operation.Result, got, testCase.want)
		}
	}
}
