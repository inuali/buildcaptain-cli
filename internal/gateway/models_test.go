package gateway

import (
	"encoding/json"
	"regexp"
	"testing"
)

// The request wire format must match what the Swift app's JSONDecoder expects
// from AgentGatewayRequest.
func TestRequestGoldenJSON(t *testing.T) {
	branch := "main"
	commit := "abc123"
	request := Request{
		Version: ProtocolVersion,
		ID:      "11111111-2222-3333-4444-555555555555",
		Action:  ActionTrigger,
		Workspace: &WorkspaceContext{
			Repository: "github.com/acme/widgets",
			Branch:     branch,
			Commit:     commit,
			Caller:     "BuildCaptain CLI",
		},
		ConfigurationID: "deploy-staging",
		Inputs:          map[string]string{"TARGET": "staging"},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"id":"11111111-2222-3333-4444-555555555555","action":"trigger",` +
		`"workspace":{"repository":"github.com/acme/widgets","branch":"main","commit":"abc123","caller":"BuildCaptain CLI"},` +
		`"configurationID":"deploy-staging","inputs":{"TARGET":"staging"}}`
	if string(data) != want {
		t.Errorf("request JSON:\n got %s\nwant %s", data, want)
	}
}

func TestRequestOmitsEmptyOptionals(t *testing.T) {
	request := Request{Version: ProtocolVersion, ID: "X", Action: ActionListConfigurations}
	data, _ := json.Marshal(request)
	for _, forbidden := range []string{"workspace", "configurationID", "inputs", "operationID"} {
		if regexp.MustCompile(forbidden).Match(data) {
			t.Errorf("empty %s should be omitted, got %s", forbidden, data)
		}
	}
}

func TestResponseDecode(t *testing.T) {
	payload := `{"version":1,"id":"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE","ok":false,"error":"nope"}`
	var response Response
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error != "nope" || response.Version != 1 {
		t.Errorf("unexpected response: %+v", response)
	}
}

func TestNormalizeUUID(t *testing.T) {
	upper, ok := NormalizeUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if !ok || upper != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" {
		t.Errorf("got %q ok=%v", upper, ok)
	}
	for _, bad := range []string{"", "not-a-uuid", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeez", "AAAAAAAABBBBCCCCDDDDEEEEEEEEEEEE"} {
		if _, ok := NormalizeUUID(bad); ok {
			t.Errorf("NormalizeUUID(%q) unexpectedly valid", bad)
		}
	}
}

func TestNewUUIDFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`)
	for range 32 {
		id := NewUUID()
		if !pattern.MatchString(id) {
			t.Fatalf("NewUUID() = %q does not match canonical v4 format", id)
		}
	}
}

func TestFormatJSONSortsKeys(t *testing.T) {
	response := failureResponse("11111111-2222-3333-4444-555555555555", "boom")
	text, err := FormatJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"error\": \"boom\",\n  \"id\": \"11111111-2222-3333-4444-555555555555\",\n  \"ok\": false,\n  \"version\": 1\n}"
	if text != want {
		t.Errorf("FormatJSON:\n got %s\nwant %s", text, want)
	}
}

// The log request's extra fields must reach the app under the names its
// decoder expects, and stay absent when unused.
func TestLogRequestGoldenJSON(t *testing.T) {
	lines := 200
	request := Request{
		Version:     ProtocolVersion,
		ID:          "11111111-2222-3333-4444-555555555555",
		Action:      ActionLog,
		OperationID: "1A2B3C4D-5E6F-4A8B-9C0D-1E2F3A4B5C6D",
		Stage:       "Tests",
		Lines:       &lines,
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"id":"11111111-2222-3333-4444-555555555555","action":"log",` +
		`"operationID":"1A2B3C4D-5E6F-4A8B-9C0D-1E2F3A4B5C6D","stage":"Tests","lines":200}`
	if string(data) != want {
		t.Errorf("request JSON:\n got %s\nwant %s", data, want)
	}
}

func TestStagesResponseDecodes(t *testing.T) {
	raw := `{"version":1,"id":"X","ok":true,"stagesComplete":true,"stages":[` +
		`{"id":"6","name":"Build","status":"success","startedAt":"2026-09-10T08:00:00Z","duration":12.5},` +
		`{"id":"9","name":"Tests","status":"failure","parent":"6"}]}`
	var response Response
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Stages) != 2 || response.StagesComplete == nil || !*response.StagesComplete {
		t.Fatalf("response = %+v", response)
	}
	build := response.Stages[0]
	if build.Status != "success" || build.Duration == nil || *build.Duration != 12.5 || build.StartedAt.ISO8601() != "2026-09-10T08:00:00Z" {
		t.Errorf("build stage = %+v", build)
	}
	if tests := response.Stages[1]; tests.Parent != "6" || tests.Duration != nil {
		t.Errorf("tests stage = %+v", tests)
	}
}
