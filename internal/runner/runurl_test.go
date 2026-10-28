package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/output"
)

const runURL = "https://jenkins.example.com/job/jobfoto-teams/job/main/481/"

func runReport(state, result string) gateway.RunReport {
	return gateway.RunReport{
		URL:               runURL,
		Number:            481,
		Job:               "jobfoto-teams » main",
		ConfigurationKey:  "jobfoto-deploy",
		ConfigurationName: "Jobfoto deploy",
		State:             state,
		Result:            result,
	}
}

// A run started by hand has no run ID; its URL is the handle, and the output
// reads exactly like an agent's run with the URL in the ID's place.
func TestStatusByRunURLReadsLikeAnAgentRun(t *testing.T) {
	fake := &fakeGateway{
		runs:   []gateway.RunReport{runReport(gateway.StateRunning, "running")},
		stages: sampleStages(),
	}
	options := fake.start(t)

	result := Status(context.Background(), options, runURL)
	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	payload := runPayload(t, result)
	if payload.RunID != runURL || payload.State != "running" || payload.Result != "running" {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Configuration != "jobfoto-deploy" || payload.Name != "Jobfoto deploy" {
		t.Errorf("configuration = %q / %q", payload.Configuration, payload.Name)
	}
	if payload.Jenkins == nil || payload.Jenkins.Job != "jobfoto-teams » main" ||
		payload.Jenkins.BuildURL != runURL || payload.Jenkins.BuildNumber == nil || *payload.Jenkins.BuildNumber != 481 {
		t.Errorf("jenkins = %+v", payload.Jenkins)
	}
	if payload.Poll != "buildcaptain status "+runURL || payload.Wait != "buildcaptain wait "+runURL {
		t.Errorf("continuations = %q / %q", payload.Poll, payload.Wait)
	}
	if payload.Logs != "buildcaptain logs "+runURL {
		t.Errorf("logs command = %q", payload.Logs)
	}
	if len(payload.Stages) != 3 || payload.Stages[2].Logs != "buildcaptain logs "+runURL+" --stage 12" {
		t.Errorf("stages = %+v", payload.Stages)
	}
	for _, request := range fake.allRequests() {
		if request.RunURL != runURL || request.OperationID != "" {
			t.Errorf("%s request sent operationID %q, runURL %q", request.Action, request.OperationID, request.RunURL)
		}
	}
}

func TestWaitByRunURLFollowsToTheResult(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{runs: []gateway.RunReport{
		runReport(gateway.StateRunning, "running"),
		runReport(gateway.StateRunning, "running"),
		runReport(gateway.StateCompleted, "failure"),
	}}
	options := fake.start(t)

	result := Wait(context.Background(), WaitOptions{Options: options, RunID: runURL})
	if code := ExitCode(result); code != ExitBuildFailed {
		t.Errorf("exit code = %d, want %d", code, ExitBuildFailed)
	}
	payload := runPayload(t, result)
	if payload.State != gateway.StateCompleted || payload.Result != "failure" || payload.RunID != runURL {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Poll != "" || payload.Wait != "" {
		t.Errorf("a finished run offers no continuation, got %q / %q", payload.Poll, payload.Wait)
	}
	if polls := fake.pollCount(); polls != 3 {
		t.Errorf("polls = %d, want 3", polls)
	}
}

func TestLogsByRunURL(t *testing.T) {
	fake := &fakeGateway{log: &gateway.BuildLog{
		Stage: &gateway.Stage{ID: "9", Name: "Tests", Status: "failure"},
		Text:  "boom\n",
	}}
	options := fake.start(t)

	result := Logs(context.Background(), LogsOptions{Options: options, RunID: runURL, Stage: "Tests", Lines: 20})
	if !result.OK {
		t.Fatalf("logs failed: %+v", result.Payload)
	}
	payload, ok := result.Payload.(output.Log)
	if !ok {
		t.Fatalf("payload = %#v", result.Payload)
	}
	if payload.RunID != runURL || payload.Text != "boom\n" || payload.Stage == nil ||
		payload.Stage.Logs != "buildcaptain logs "+runURL+" --stage 9" {
		t.Errorf("payload = %+v", payload)
	}
	last := fake.lastRequest()
	if last.Action != gateway.ActionLog || last.RunURL != runURL || last.OperationID != "" ||
		last.Stage != "Tests" || last.Lines == nil || *last.Lines != 20 {
		t.Errorf("request = %+v", last)
	}
}

// An app that predates run URLs ignores the field and reports a missing
// operation, which would send the caller hunting for an ID it never had.
func TestRunURLOnAnOldAppAsksForANewerApp(t *testing.T) {
	fake := &fakeGateway{legacy: true}
	options := fake.start(t)

	for name, result := range map[string]Result{
		"status": Status(context.Background(), options, runURL),
		"wait":   Wait(context.Background(), WaitOptions{Options: options, RunID: runURL}),
		"logs":   Logs(context.Background(), LogsOptions{Options: options, RunID: runURL}),
	} {
		failure, ok := result.Payload.(output.Failure)
		if !ok || result.OK {
			t.Fatalf("%s: expected a failure, got %#v", name, result.Payload)
		}
		if !strings.Contains(failure.Error, "needs a newer BuildCaptain") {
			t.Errorf("%s: error = %q", name, failure.Error)
		}
		if strings.Contains(failure.Hint, "recent runs") {
			t.Errorf("%s: hint should not point at run IDs, got %q", name, failure.Hint)
		}
	}
}

// The oldest apps do not know the log action at all and close silently.
func TestRunURLOnASilentOldAppAsksForANewerApp(t *testing.T) {
	fake := &fakeGateway{silent: map[gateway.Action]bool{gateway.ActionLog: true}}
	options := fake.start(t)

	result := Logs(context.Background(), LogsOptions{Options: options, RunID: runURL})
	failure, ok := result.Payload.(output.Failure)
	if !ok || !strings.Contains(failure.Error, "needs a newer BuildCaptain") {
		t.Errorf("payload = %#v", result.Payload)
	}
}

// The app owns the security decision; its refusal reaches the caller as is.
func TestRunURLRefusalIsRelayed(t *testing.T) {
	const refusal = "No enabled Agent Access run configuration for this repository covers this run's job."
	fake := &fakeGateway{runURLErr: refusal}
	options := fake.start(t)

	result := Status(context.Background(), options, runURL)
	failure, ok := result.Payload.(output.Failure)
	if !ok || failure.Error != refusal || failure.Hint != "" {
		t.Errorf("payload = %#v", result.Payload)
	}
	if code := ExitCode(result); code != ExitRequestFailed {
		t.Errorf("exit code = %d, want %d", code, ExitRequestFailed)
	}
}

// A run URL that could never pass is refused before anything is sent.
func TestMalformedRunURLIsRefusedLocally(t *testing.T) {
	fake := &fakeGateway{runs: []gateway.RunReport{runReport(gateway.StateRunning, "running")}}
	options := fake.start(t)

	for _, value := range []string{
		"https://",
		"https:///job/x/1/",
		runURL + "?token=1",
		runURL + "#top",
		"https://user:secret@jenkins.example.com/job/x/1/",
		"https://jenkins.example.com/job/a b/1/",
	} {
		result := Status(context.Background(), options, value)
		failure, ok := result.Payload.(output.Failure)
		if !ok || !strings.Contains(failure.Error, "is not a Jenkins run URL") {
			t.Errorf("%q: payload = %#v", value, result.Payload)
		}
	}
	if requests := fake.allRequests(); len(requests) != 0 {
		t.Errorf("sent %d requests for malformed URLs", len(requests))
	}
}
