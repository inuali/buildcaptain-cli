package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/output"
)

func startedOperation(state, result string) gateway.Operation {
	op := operation(state, result)
	op.RunID = &gateway.JenkinsRun{Number: 42, URL: "https://jenkins.example/job/widgets/42/"}
	return op
}

func sampleStages() []gateway.Stage {
	duration := 12.5
	return []gateway.Stage{
		{ID: "6", Name: "Build", Status: "success", Duration: &duration},
		{ID: "9", Name: "Tests", Status: "failure", Duration: &duration},
		{ID: "12", Name: "Unit tests", Status: "failure", Parent: "9"},
	}
}

// The reason stages exist in the output: a failed build says which stage
// broke and hands over the command that shows why.
func TestStatusListsStagesWithLogsCommands(t *testing.T) {
	fake := &fakeGateway{
		states: []gateway.Operation{startedOperation(gateway.StateCompleted, "failure")},
		stages: sampleStages(),
	}
	options := fake.start(t)

	result := Status(context.Background(), options, operationUUID)
	if code := ExitCode(result); code != ExitBuildFailed {
		t.Errorf("exit code = %d, want %d", code, ExitBuildFailed)
	}
	payload := runPayload(t, result)
	if len(payload.Stages) != 3 {
		t.Fatalf("stages = %+v, want 3", payload.Stages)
	}
	if payload.StagesUnavailable != "" {
		t.Errorf("stages_unavailable = %q, want empty", payload.StagesUnavailable)
	}
	tests := payload.Stages[1]
	if tests.Name != "Tests" || tests.Status != "failure" || tests.DurationSeconds == nil || *tests.DurationSeconds != 12.5 {
		t.Errorf("stage = %+v", tests)
	}
	if tests.Logs != "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d --stage Tests" {
		t.Errorf("logs command = %q", tests.Logs)
	}
	if nested := payload.Stages[2]; nested.Parent != "9" || nested.Logs != "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d --stage 'Unit tests'" {
		t.Errorf("nested stage = %+v", nested)
	}
	if payload.Logs != "buildcaptain logs run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d" {
		t.Errorf("run logs command = %q", payload.Logs)
	}
}

// Before Jenkins starts the build there is no graph to ask for, so the stage
// round trip is skipped rather than answered with an empty list.
func TestStagesAreNotRequestedBeforeTheBuildStarts(t *testing.T) {
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateQueued, "")}}
	options := fake.start(t)

	payload := runPayload(t, Status(context.Background(), options, operationUUID))
	if payload.Stages != nil || payload.Logs != "" {
		t.Errorf("queued run should carry no stages or logs command, got %+v", payload)
	}
	if last := fake.lastRequest(); last.Action != gateway.ActionStatus {
		t.Errorf("last request = %s, want status", last.Action)
	}
}

// An app older than the stages action closes the connection silently. The
// build's own result is still known, so the command succeeds and says why the
// stage list is missing — without the five-second relaunch dance.
func TestOldAppExplainsMissingStages(t *testing.T) {
	fake := &fakeGateway{
		states: []gateway.Operation{startedOperation(gateway.StateCompleted, "success")},
		silent: map[gateway.Action]bool{gateway.ActionStages: true},
	}
	options := fake.start(t)

	result := Status(context.Background(), options, operationUUID)
	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	payload := runPayload(t, result)
	if payload.Stages != nil {
		t.Errorf("stages = %+v, want none", payload.Stages)
	}
	if !strings.Contains(payload.StagesUnavailable, "too old") {
		t.Errorf("stages_unavailable = %q, want the old-app explanation", payload.StagesUnavailable)
	}
}

func TestLogsPassesStageAndTailThrough(t *testing.T) {
	stage := sampleStages()[1]
	fake := &fakeGateway{
		states: []gateway.Operation{startedOperation(gateway.StateCompleted, "failure")},
		log:    &gateway.BuildLog{Stage: &stage, Text: "FAILED: 3 tests\n", Truncated: true},
	}
	options := fake.start(t)

	result := Logs(context.Background(), LogsOptions{Options: options, RunID: operationUUID, Stage: "tests", Lines: 50})
	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	payload, ok := result.Payload.(output.Log)
	if !ok {
		t.Fatalf("expected a log payload, got %#v", result.Payload)
	}
	if payload.Text != "FAILED: 3 tests\n" || !payload.Truncated {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Stage == nil || payload.Stage.Name != "Tests" || payload.Stage.ID != "9" {
		t.Errorf("stage = %+v, want the stage the app resolved", payload.Stage)
	}
	if payload.RunID != "run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d" {
		t.Errorf("run_id = %q", payload.RunID)
	}
	last := fake.lastRequest()
	if last.Action != gateway.ActionLog || last.Stage != "tests" || last.Lines == nil || *last.Lines != 50 {
		t.Errorf("request = %+v, want the stage and line count forwarded", last)
	}
}

func TestLogsWithoutTailAsksForEverything(t *testing.T) {
	fake := &fakeGateway{
		states: []gateway.Operation{startedOperation(gateway.StateCompleted, "success")},
		log:    &gateway.BuildLog{Text: "Finished: SUCCESS\n"},
	}
	options := fake.start(t)

	payload, ok := Logs(context.Background(), LogsOptions{Options: options, RunID: operationUUID}).Payload.(output.Log)
	if !ok || payload.Stage != nil || payload.Truncated {
		t.Errorf("payload = %+v", payload)
	}
	if last := fake.lastRequest(); last.Stage != "" || last.Lines != nil {
		t.Errorf("request = %+v, want no stage and no line limit", last)
	}
}

func TestLogsRelaysTheAppsRefusal(t *testing.T) {
	fake := &fakeGateway{
		states: []gateway.Operation{startedOperation(gateway.StateCompleted, "failure")},
		logErr: `No stage is named "Deploy". The stages are: "Build", "Tests".`,
	}
	options := fake.start(t)

	result := Logs(context.Background(), LogsOptions{Options: options, RunID: operationUUID, Stage: "Deploy"})
	if code := ExitCode(result); code != ExitRequestFailed {
		t.Errorf("exit code = %d, want %d", code, ExitRequestFailed)
	}
	failure, ok := result.Payload.(output.Failure)
	if !ok || !strings.Contains(failure.Error, `No stage is named "Deploy"`) {
		t.Errorf("payload = %#v", result.Payload)
	}
}

func TestLogsOnOldAppFailsFast(t *testing.T) {
	fake := &fakeGateway{
		states: []gateway.Operation{startedOperation(gateway.StateCompleted, "failure")},
		silent: map[gateway.Action]bool{gateway.ActionLog: true},
	}
	options := fake.start(t)

	result := Logs(context.Background(), LogsOptions{Options: options, RunID: operationUUID})
	failure, ok := result.Payload.(output.Failure)
	if !ok || !strings.Contains(failure.Error, "too old") {
		t.Errorf("payload = %#v, want the old-app explanation", result.Payload)
	}
}
