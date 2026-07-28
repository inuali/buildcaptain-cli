package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/output"
	"github.com/inuali/buildcaptain-cli/internal/runs"
)

const operationUUID = "1A2B3C4D-5E6F-4A8B-9C0D-1E2F3A4B5C6D"

// fakeGateway stands in for the app: it answers one request per connection,
// walking a scripted sequence of operation states as status is polled.
type fakeGateway struct {
	mutex      sync.Mutex
	states     []gateway.Operation
	polls      int
	configs    []gateway.PublicConfiguration
	triggerErr string
	statusErr  string
	// failStatusPolls makes the next N status polls fail, simulating the app
	// restarting mid-build.
	failStatusPolls int
}

func (f *fakeGateway) handle(request gateway.Request) gateway.Response {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	response := gateway.Response{Version: gateway.ProtocolVersion, ID: request.ID, OK: true}
	switch request.Action {
	case gateway.ActionListConfigurations:
		response.Configurations = f.configs
	case gateway.ActionTrigger:
		if f.triggerErr != "" {
			return gateway.Response{Version: gateway.ProtocolVersion, ID: request.ID, Error: f.triggerErr}
		}
		operation := f.states[0]
		response.Operation = &operation
	case gateway.ActionStatus:
		if f.failStatusPolls > 0 {
			f.failStatusPolls--
			return gateway.Response{Version: gateway.ProtocolVersion, ID: request.ID, Error: "BuildCaptain is restarting."}
		}
		if f.statusErr != "" {
			return gateway.Response{Version: gateway.ProtocolVersion, ID: request.ID, Error: f.statusErr}
		}
		f.polls++
		index := min(f.polls, len(f.states)-1)
		operation := f.states[index]
		response.Operation = &operation
	}
	return response
}

func (f *fakeGateway) pollCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.polls
}

// start listens on a socket the gateway client will find, and points the
// CLI's state directory somewhere disposable.
func (f *fakeGateway) start(t *testing.T) Options {
	t.Helper()
	directory := t.TempDir()
	// t.TempDir() embeds the test name, which pushes the socket path past the
	// 104-byte sun_path limit; bind from a short directory instead.
	socketDir, err := os.MkdirTemp("", "bc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "gateway.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request gateway.Request
				if json.Unmarshal(line, &request) != nil {
					return
				}
				payload, _ := json.Marshal(f.handle(request))
				conn.Write(append(payload, '\n'))
			}()
		}
	}()

	t.Setenv("BUILDCAPTAIN_GATEWAY_SOCKET", socketPath)
	t.Setenv("BUILDCAPTAIN_STATE_DIR", filepath.Join(directory, "state"))
	// Outside a Git repository the workspace context is empty, which is all
	// the fake gateway needs.
	return Options{WorkspaceDir: directory, Caller: "test"}
}

// fastPolling shrinks the wait loop's pacing so a multi-state build runs in
// milliseconds.
func fastPolling(t *testing.T) {
	t.Helper()
	previous := []time.Duration{initialInterval, mediumInterval, slowInterval, mediumAfter, slowAfter}
	initialInterval, mediumInterval, slowInterval = time.Millisecond, time.Millisecond, time.Millisecond
	mediumAfter, slowAfter = time.Hour, time.Hour
	t.Cleanup(func() {
		initialInterval, mediumInterval, slowInterval = previous[0], previous[1], previous[2]
		mediumAfter, slowAfter = previous[3], previous[4]
	})
}

func operation(state, result string) gateway.Operation {
	return gateway.Operation{
		ID:                operationUUID,
		ConfigurationKey:  "deploy-staging",
		ConfigurationName: "Deploy staging",
		State:             state,
		Result:            result,
		Inputs:            map[string]string{"TARGET": "staging"},
	}
}

func runPayload(t *testing.T, result Result) output.Run {
	t.Helper()
	payload, ok := result.Payload.(output.Run)
	if !ok {
		t.Fatalf("expected a run payload, got %#v", result.Payload)
	}
	return payload
}

// The headline behaviour: run follows the build to the end, so the caller
// never has to hold an ID or make a second call.
func TestTriggerWaitsForCompletion(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{
		operation(gateway.StateSubmitting, ""),
		operation(gateway.StateQueued, ""),
		operation(gateway.StateRunning, ""),
		operation(gateway.StateCompleted, "success"),
	}}
	options := fake.start(t)

	var progress []string
	result := Trigger(context.Background(), TriggerOptions{
		Options:       options,
		Configuration: "deploy-staging",
		Wait:          true,
		Timeout:       10 * time.Second,
		Progress: func(op *gateway.Operation) {
			progress = append(progress, op.StateDescription())
		},
	})

	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	payload := runPayload(t, result)
	if payload.State != gateway.StateCompleted || payload.Result != "success" {
		t.Errorf("payload state = %s/%s", payload.State, payload.Result)
	}
	// A finished build has no next step, so no poll command is offered.
	if payload.Poll != "" {
		t.Errorf("finished run should not suggest polling, got %q", payload.Poll)
	}
	want := []string{"submitting", "queued", "running", "completed (success)"}
	if strings.Join(progress, ",") != strings.Join(want, ",") {
		t.Errorf("progress = %v, want %v", progress, want)
	}
}

func TestTriggerReportsFailedBuildWithExitCode(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{
		operation(gateway.StateRunning, ""),
		operation(gateway.StateCompleted, "failure"),
	}}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: 10 * time.Second,
	})
	if code := ExitCode(result); code != ExitBuildFailed {
		t.Errorf("exit code = %d, want %d for a failed build", code, ExitBuildFailed)
	}
	if !result.OK {
		t.Error("a failed build is still a successful request")
	}
}

func TestTriggerRejectedByApprovalExits2(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{
		operation(gateway.StateAwaitingApproval, ""),
		operation(gateway.StateRejected, ""),
	}}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: 10 * time.Second,
	})
	if code := ExitCode(result); code != ExitBuildFailed {
		t.Errorf("exit code = %d, want %d for a rejected run", code, ExitBuildFailed)
	}
}

// --no-wait must hand back exactly one usable ID and the command to use it.
func TestTriggerWithoutWaitingReturnsOneIDAndACommand(t *testing.T) {
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateQueued, "")}}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: false,
	})
	payload := runPayload(t, result)
	if payload.RunID != runs.Format(operationUUID) {
		t.Errorf("run_id = %q", payload.RunID)
	}
	if payload.Poll != "buildcaptain status "+payload.RunID {
		t.Errorf("poll = %q, want the literal status command", payload.Poll)
	}
	if fake.pollCount() != 0 {
		t.Errorf("--no-wait polled %d times", fake.pollCount())
	}
	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}

	// The printed document must carry exactly one identifier.
	encoded, err := output.Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"id"`, `"version"`, `"configurationID"`, `"operation_id"`} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("output still carries %s:\n%s", forbidden, encoded)
		}
	}
	if count := strings.Count(encoded, `"run_id"`); count != 1 {
		t.Errorf("expected one run_id, found %d:\n%s", count, encoded)
	}
}

func TestTriggerTimesOutWithoutFailingTheBuild(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateRunning, "")}}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: 10 * time.Millisecond,
	})
	if code := ExitCode(result); code != ExitTimedOut {
		t.Errorf("exit code = %d, want %d", code, ExitTimedOut)
	}
	payload := runPayload(t, result)
	if !payload.TimedOut {
		t.Error("payload should say the wait timed out")
	}
	// A timeout is where the poll hint matters most: the build is still going.
	if payload.Poll == "" {
		t.Error("a timed-out run must say how to keep watching")
	}
}

func TestTriggerRemembersRunBeforeWaiting(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{
		operation(gateway.StateRunning, ""),
		operation(gateway.StateCompleted, "success"),
	}}
	options := fake.start(t)

	Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: 10 * time.Second,
	})
	// Having been remembered, the run is reachable by abbreviation and by
	// omitting the ID entirely.
	for _, reference := range []string{"", "run_1a2b3c"} {
		if resolved, err := runs.Resolve(reference); err != nil || resolved != operationUUID {
			t.Errorf("Resolve(%q) = %q, %v", reference, resolved, err)
		}
	}
}

func TestTriggerRejectionExplainsWhereToLook(t *testing.T) {
	fake := &fakeGateway{triggerErr: "This run configuration is not allowed for the current repository."}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "nope", Wait: true,
	})
	if result.OK || ExitCode(result) != ExitRequestFailed {
		t.Errorf("rejected trigger should fail the request, got %+v", result)
	}
	failure, ok := result.Payload.(output.Failure)
	if !ok {
		t.Fatalf("expected a failure payload, got %#v", result.Payload)
	}
	if !strings.Contains(failure.Hint, "buildcaptain configs") {
		t.Errorf("hint should point at configs, got %q", failure.Hint)
	}
}

// wait is the resumable half of run: same following, same exit codes, but
// reached from a run ID after an earlier wait was cut short.
func TestWaitFollowsAnAlreadyStartedRun(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{
		operation(gateway.StateQueued, ""),
		operation(gateway.StateRunning, ""),
		operation(gateway.StateCompleted, "success"),
	}}
	options := fake.start(t)
	runs.Remember(operationUUID, "deploy-staging")

	var progress []string
	result := Wait(context.Background(), WaitOptions{
		Options: options,
		RunID:   "run_1a2b3c",
		Timeout: 10 * time.Second,
		Progress: func(op *gateway.Operation) {
			progress = append(progress, op.StateDescription())
		},
	})
	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if payload := runPayload(t, result); payload.State != gateway.StateCompleted {
		t.Errorf("state = %s", payload.State)
	}
	if len(progress) < 2 || progress[len(progress)-1] != "completed (success)" {
		t.Errorf("progress = %v", progress)
	}
}

func TestWaitOnAFinishedRunReturnsImmediately(t *testing.T) {
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateCompleted, "failure")}}
	options := fake.start(t)
	runs.Remember(operationUUID, "deploy-staging")

	result := Wait(context.Background(), WaitOptions{
		Options: options, RunID: "", Timeout: time.Hour,
	})
	if code := ExitCode(result); code != ExitBuildFailed {
		t.Errorf("exit code = %d, want %d", code, ExitBuildFailed)
	}
	// One read to learn the state, and no follow-up polling after it.
	if fake.pollCount() != 1 {
		t.Errorf("a finished run should need one read, got %d", fake.pollCount())
	}
}

func TestWaitTimesOutLikeRun(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateRunning, "")}}
	options := fake.start(t)
	runs.Remember(operationUUID, "deploy-staging")

	result := Wait(context.Background(), WaitOptions{
		Options: options, RunID: "", Timeout: 10 * time.Millisecond,
	})
	if code := ExitCode(result); code != ExitTimedOut {
		t.Errorf("exit code = %d, want %d", code, ExitTimedOut)
	}
	if payload := runPayload(t, result); payload.Wait == "" {
		t.Error("a timed-out wait must say how to resume")
	}
}

// The run ID has to reach the caller before any waiting: a process killed
// mid-wait has then already been told which build it started.
func TestTriggerAnnouncesTheRunBeforeWaiting(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{
		operation(gateway.StateRunning, ""),
		operation(gateway.StateCompleted, "success"),
	}}
	options := fake.start(t)

	announced := ""
	polledBeforeAnnouncement := -1
	Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: 10 * time.Second,
		Accepted: func(op *gateway.Operation) {
			announced = runs.Format(op.ID)
			polledBeforeAnnouncement = fake.pollCount()
		},
	})
	if announced != runs.Format(operationUUID) {
		t.Errorf("announced %q, want %q", announced, runs.Format(operationUUID))
	}
	if polledBeforeAnnouncement != 0 {
		t.Errorf("announcement came after %d polls; it must precede the wait", polledBeforeAnnouncement)
	}
}

// An active run offers both continuations: one that checks and returns, one
// that blocks until there is an answer.
func TestActiveRunOffersBothContinuations(t *testing.T) {
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateQueued, "")}}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: false,
	})
	payload := runPayload(t, result)
	if payload.Poll != "buildcaptain status "+payload.RunID {
		t.Errorf("poll = %q", payload.Poll)
	}
	if payload.Wait != "buildcaptain wait "+payload.RunID {
		t.Errorf("wait = %q", payload.Wait)
	}
}

func TestStatusResolvesAbbreviatedRunID(t *testing.T) {
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateCompleted, "success")}}
	options := fake.start(t)
	runs.Remember(operationUUID, "deploy-staging")

	result := Status(context.Background(), options, "run_1a2b3c4d")
	if !result.OK {
		t.Fatalf("status failed: %+v", result.Payload)
	}
	if payload := runPayload(t, result); payload.RunID != runs.Format(operationUUID) {
		t.Errorf("run_id = %q", payload.RunID)
	}
}

func TestStatusOnAConfigurationKeyExplainsItself(t *testing.T) {
	fake := &fakeGateway{}
	options := fake.start(t)

	result := Status(context.Background(), options, "deploy-staging")
	if result.OK {
		t.Fatal("a configuration key must not be accepted as a run ID")
	}
	failure := result.Payload.(output.Failure)
	if !strings.Contains(failure.Error, "buildcaptain run") {
		t.Errorf("error should redirect to run, got %q", failure.Error)
	}
	// It must not have reached the gateway at all.
	if fake.pollCount() != 0 {
		t.Error("a malformed run ID should fail before the round trip")
	}
}

func TestConfigsNameTheKeyTheSameWayRunDoes(t *testing.T) {
	pattern := "release/.*"
	fake := &fakeGateway{configs: []gateway.PublicConfiguration{{
		ID:       "deploy-staging",
		Name:     "Deploy staging",
		Approval: "requiresApproval",
		Inputs: []gateway.PublicInput{
			{Name: "branch", Type: "string", Pattern: &pattern},
			{Name: "TARGET", Type: "choice", AllowedValues: []string{"staging", "canary"}},
		},
	}}}
	options := fake.start(t)

	result := Configs(options)
	if !result.OK {
		t.Fatalf("configs failed: %+v", result.Payload)
	}
	payload := result.Payload.(output.Configs)
	if len(payload.Configurations) != 1 {
		t.Fatalf("got %d configurations", len(payload.Configurations))
	}
	entry := payload.Configurations[0]
	if entry.Configuration != "deploy-staging" {
		t.Errorf("configuration = %q", entry.Configuration)
	}
	want := "buildcaptain run deploy-staging --input branch=<value> --input TARGET=<value>"
	if entry.Run != want {
		t.Errorf("run command = %q, want %q", entry.Run, want)
	}
	if entry.Inputs[0].Pattern != pattern {
		t.Errorf("pattern = %q", entry.Inputs[0].Pattern)
	}
}

// Verbose is the escape hatch for the wire format the CLI no longer relays.
func TestVerboseKeepsTheWireFormAvailable(t *testing.T) {
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateQueued, "")}}
	options := fake.start(t)
	options.Verbose = true

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: false,
	})
	payload := runPayload(t, result)
	if len(payload.Raw) == 0 {
		t.Fatal("--verbose should include the raw response")
	}
	var raw map[string]any
	if err := json.Unmarshal(payload.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "id", "operation"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("raw response is missing %q: %s", key, payload.Raw)
		}
	}
}

// A poll that fails mid-build is usually the app restarting; the wait must
// ride it out rather than reporting a failure the build did not have.
func TestWaitSurvivesATransientPollFailure(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{
		states: []gateway.Operation{
			operation(gateway.StateRunning, ""),
			operation(gateway.StateCompleted, "success"),
		},
		failStatusPolls: maxConsecutivePollFailures - 1,
	}
	options := fake.start(t)

	result := Trigger(context.Background(), TriggerOptions{
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: 5 * time.Second,
	})
	if code := ExitCode(result); code != ExitOK {
		t.Errorf("exit code = %d, want %d once polling recovered", code, ExitOK)
	}
	if payload := runPayload(t, result); payload.Result != "success" {
		t.Errorf("result = %q after recovering", payload.Result)
	}
}

// A sustained outage must not swallow the rest of the timeout in silence.
func TestWaitGivesUpWhenTheAppStaysAway(t *testing.T) {
	fastPolling(t)
	fake := &fakeGateway{states: []gateway.Operation{operation(gateway.StateRunning, "")}}
	options := fake.start(t)
	fake.statusErr = "BuildCaptain is not running."

	result := Trigger(context.Background(), TriggerOptions{
		// A timeout far longer than the test's patience: giving up has to come
		// from the failure count, not the deadline.
		Options: options, Configuration: "deploy-staging", Wait: true, Timeout: time.Hour,
	})
	if result.OK {
		t.Fatal("losing contact should be reported as a failure")
	}
	failure, ok := result.Payload.(output.Failure)
	if !ok {
		t.Fatalf("expected a failure payload, got %#v", result.Payload)
	}
	// The build is still out there, so the caller must be told how to resume.
	if !strings.Contains(failure.Hint, "buildcaptain wait run_") {
		t.Errorf("hint should carry the resume command, got %q", failure.Hint)
	}
	if !strings.Contains(failure.Error, "still be running") {
		t.Errorf("error should not claim the build failed, got %q", failure.Error)
	}
}

func TestMain(m *testing.M) {
	// Never let a test wake the real app if a socket lookup goes wrong.
	os.Setenv("BUILDCAPTAIN_REPO", "")
	os.Exit(m.Run())
}
