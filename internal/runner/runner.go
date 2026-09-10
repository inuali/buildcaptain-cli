// Package runner performs the CLI's real operations — list, trigger, status,
// logs — and turns each into the printed shape. Both the command line and
// the MCP server go through here so an agent sees the same result whichever
// way it reached BuildCaptain.
package runner

import (
	"context"
	"time"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/output"
	"github.com/inuali/buildcaptain-cli/internal/runs"
	"github.com/inuali/buildcaptain-cli/internal/workspace"
)

// DefaultTimeout bounds a waiting `run`. Builds that take longer are not
// failures, so hitting it reports the live state and the command to poll with.
const DefaultTimeout = 30 * time.Minute

// Poll pacing: fast while a caller is still watching the first transitions,
// then slower, since a running build changes state rarely. Variables rather
// than constants so tests can run the wait loop without real waiting.
var (
	initialInterval = time.Second
	mediumInterval  = 2 * time.Second
	slowInterval    = 5 * time.Second
	mediumAfter     = 15 * time.Second
	slowAfter       = time.Minute
)

// maxConsecutivePollFailures bounds how long a wait tolerates losing contact
// with the app. Every send already retries internally for five seconds and
// tries to relaunch BuildCaptain, so this is roughly fifteen seconds of real
// absence — long enough to ride out a restart, short enough that a quit app
// does not leave the caller waiting out the whole timeout in silence.
const maxConsecutivePollFailures = 3

// Result is one command's outcome: the payload to print plus what the caller
// needs to choose an exit code.
type Result struct {
	Payload   any
	OK        bool
	TimedOut  bool
	Operation *gateway.Operation
}

// Options are the settings shared by every call.
type Options struct {
	WorkspaceDir string
	Caller       string
	Verbose      bool
}

// TriggerOptions describes one build request.
type TriggerOptions struct {
	Options
	Configuration string
	Inputs        map[string]string
	// Wait follows the build to a terminal state instead of returning as soon
	// as BuildCaptain has accepted it.
	Wait    bool
	Timeout time.Duration
	// Progress is called once per observed state change while waiting.
	Progress func(*gateway.Operation)
	// Accepted is called once, before any waiting, as soon as BuildCaptain has
	// taken the build. It is what lets a caller whose process is killed
	// mid-wait still have the run ID.
	Accepted func(*gateway.Operation)
}

// WaitOptions describes a wait on a run that is already under way.
type WaitOptions struct {
	Options
	// RunID may be any accepted spelling, or empty for the most recent run.
	RunID    string
	Timeout  time.Duration
	Progress func(*gateway.Operation)
}

// LogsOptions describes one request for console output.
type LogsOptions struct {
	Options
	// RunID may be any accepted spelling, or empty for the most recent run.
	RunID string
	// Stage is a stage name or id; empty asks for the whole build's console.
	Stage string
	// Lines keeps only that many lines from the end; 0 keeps everything the
	// app's size cap allows.
	Lines int
}

// followSpec is the part of a request the wait loop needs, shared by the
// commands that start a build and the ones that only watch it.
type followSpec struct {
	options  Options
	timeout  time.Duration
	progress func(*gateway.Operation)
}

// Configs lists the run configurations allowed here.
func Configs(options Options) Result {
	response := send(options, gateway.SendParams{Action: gateway.ActionListConfigurations})
	if !response.OK {
		return failure(response, options.Verbose, "")
	}
	payload := output.NewConfigs(response.Configurations)
	if options.Verbose {
		payload.Raw = rawOf(response)
	}
	return Result{Payload: payload, OK: true}
}

// Trigger starts a build and, unless asked not to, follows it to completion.
func Trigger(ctx context.Context, options TriggerOptions) Result {
	response := send(options.Options, gateway.SendParams{
		Action:          gateway.ActionTrigger,
		ConfigurationID: options.Configuration,
		Inputs:          options.Inputs,
	})
	if !response.OK {
		return failure(response, options.Verbose, "Run buildcaptain configs to see what this repository is allowed to start.")
	}
	operation := response.Operation
	if operation == nil {
		return Result{
			Payload: output.NewFailure("BuildCaptain accepted the request but reported no run.", ""),
			OK:      false,
		}
	}
	// Remembered before any waiting, so an interrupted wait still leaves the
	// caller able to name this run.
	runs.Remember(operation.ID, operation.ConfigurationKey)
	if options.Accepted != nil {
		options.Accepted(operation)
	}

	timedOut, lostContact := false, false
	if options.Wait {
		if options.Progress != nil {
			options.Progress(operation)
		}
		operation, timedOut, lostContact, response = follow(ctx, followSpec{
			options:  options.Options,
			timeout:  options.Timeout,
			progress: options.Progress,
		}, operation, response)
	}
	if lostContact {
		return lostContactResult(operation)
	}
	return runResult(operation, response, options.Options, timedOut)
}

// Wait follows a run that is already under way to a terminal state. It is the
// resumable half of Trigger: same waiting, same exit codes, but reached from a
// run ID rather than from starting the build.
func Wait(ctx context.Context, options WaitOptions) Result {
	operationUUID, err := runs.Resolve(options.RunID)
	if err != nil {
		return Result{Payload: output.NewFailure(err.Error(), ""), OK: false}
	}
	response := send(options.Options, gateway.SendParams{
		Action:      gateway.ActionStatus,
		OperationID: operationUUID,
	})
	if !response.OK {
		return failure(response, options.Verbose,
			"BuildCaptain only keeps recent runs. Start a new one with buildcaptain run <configuration>.")
	}
	operation := response.Operation
	if operation == nil {
		return Result{Payload: output.NewFailure("BuildCaptain returned no run for that ID.", ""), OK: false}
	}
	if options.Progress != nil {
		options.Progress(operation)
	}
	operation, timedOut, lostContact, response := follow(ctx, followSpec{
		options:  options.Options,
		timeout:  options.Timeout,
		progress: options.Progress,
	}, operation, response)
	if lostContact {
		return lostContactResult(operation)
	}
	return runResult(operation, response, options.Options, timedOut)
}

// lostContactResult reports a wait that ran out of patience with a missing
// app. The build itself is unaffected, so it says so and hands back the
// command that picks the thread back up.
func lostContactResult(operation *gateway.Operation) Result {
	return Result{
		Payload: output.NewFailure(
			"Lost contact with BuildCaptain while waiting. The build may still be running.",
			"Once the app is running again: "+output.ResumeCommand(operation)),
		OK: false,
	}
}

// Status reports on one run. An empty runID means the most recent one.
func Status(ctx context.Context, options Options, runID string) Result {
	operationUUID, err := runs.Resolve(runID)
	if err != nil {
		return Result{Payload: output.NewFailure(err.Error(), ""), OK: false}
	}
	response := send(options, gateway.SendParams{Action: gateway.ActionStatus, OperationID: operationUUID})
	if !response.OK {
		return failure(response, options.Verbose,
			"BuildCaptain only keeps recent runs. Start a new one with buildcaptain run <configuration>.")
	}
	if response.Operation == nil {
		return Result{Payload: output.NewFailure("BuildCaptain returned no run for that ID.", ""), OK: false}
	}
	return runResult(response.Operation, response, options, false)
}

// Logs fetches console output: the whole build's, or one stage's. An empty
// runID means the most recent run.
func Logs(ctx context.Context, options LogsOptions) Result {
	operationUUID, err := runs.Resolve(options.RunID)
	if err != nil {
		return Result{Payload: output.NewFailure(err.Error(), ""), OK: false}
	}
	params := gateway.SendParams{Action: gateway.ActionLog, OperationID: operationUUID, Stage: options.Stage}
	if options.Lines > 0 {
		lines := options.Lines
		params.Lines = &lines
	}
	response := send(options.Options, params)
	if !response.OK {
		return failure(response, options.Verbose,
			"Check the run with buildcaptain status; its stages list the exact logs command for each stage.")
	}
	if response.Log == nil {
		return Result{Payload: output.NewFailure("BuildCaptain returned no log for that run.", ""), OK: false}
	}
	payload := output.NewLog(operationUUID, response.Log)
	if options.Verbose {
		payload.Raw = rawOf(response)
	}
	return Result{Payload: payload, OK: true}
}

// follow polls until the operation stops moving, the deadline passes, contact
// with the app is lost, or the context is cancelled. The last good response is
// kept so --verbose shows the wire form of the state actually reported.
func follow(
	ctx context.Context,
	spec followSpec,
	operation *gateway.Operation,
	response gateway.Response,
) (result *gateway.Operation, timedOut, lostContact bool, last gateway.Response) {
	if !operation.IsActive() {
		return operation, false, false, response
	}
	deadline := time.Time{}
	if spec.timeout > 0 {
		deadline = time.Now().Add(spec.timeout)
	}
	started := time.Now()
	state := operation.State + "/" + operation.Result
	failures := 0

	for {
		interval := initialInterval
		switch elapsed := time.Since(started); {
		case elapsed > slowAfter:
			interval = slowInterval
		case elapsed > mediumAfter:
			interval = mediumInterval
		}
		select {
		case <-ctx.Done():
			// Interrupted rather than finished: the build carries on, which
			// is the same situation a timeout leaves the caller in.
			return operation, true, false, response
		case <-time.After(interval):
		}

		polled := send(spec.options, gateway.SendParams{
			Action:      gateway.ActionStatus,
			OperationID: operation.ID,
		})
		// A failed poll is usually the app restarting or briefly busy; the
		// build is unaffected, so keep the last known state and try again —
		// but not forever.
		if !polled.OK || polled.Operation == nil {
			if failures++; failures >= maxConsecutivePollFailures {
				return operation, false, true, response
			}
		} else {
			failures = 0
			operation, response = polled.Operation, polled
			if current := operation.State + "/" + operation.Result; current != state {
				state = current
				if spec.progress != nil {
					spec.progress(operation)
				}
			}
			if !operation.IsActive() {
				return operation, false, false, response
			}
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return operation, true, false, response
		}
	}
}

func runResult(
	operation *gateway.Operation,
	response gateway.Response,
	options Options,
	timedOut bool,
) Result {
	payload := output.NewRun(operation)
	payload.TimedOut = timedOut
	// One extra round trip per command, not per poll: the stage graph is
	// worth reading once the answer is being printed, not while waiting.
	if operation.RunID != nil {
		payload.Stages, payload.StagesUnavailable = stagesOf(options, operation)
	}
	if options.Verbose {
		payload.Raw = rawOf(response)
	}
	return Result{Payload: payload, OK: true, TimedOut: timedOut, Operation: operation}
}

// stagesOf reads the stage graph for a started build. Failing to get it must
// not fail the command — the build's own result is already known — so the
// reason travels alongside the (missing) list instead.
func stagesOf(options Options, operation *gateway.Operation) ([]output.Stage, string) {
	response := send(options, gateway.SendParams{Action: gateway.ActionStages, OperationID: operation.ID})
	if !response.OK {
		message := response.Error
		if message == "" {
			message = "BuildCaptain did not report the stages."
		}
		return nil, message
	}
	return output.NewStages(operation.ID, response.Stages), ""
}

func failure(response gateway.Response, verbose bool, hint string) Result {
	message := response.Error
	if message == "" {
		message = "BuildCaptain rejected the request."
	}
	payload := output.NewFailure(message, hint)
	if verbose {
		payload.Raw = rawOf(response)
	}
	return Result{Payload: payload, OK: false}
}

func rawOf(response gateway.Response) []byte {
	if len(response.Raw) > 0 {
		return response.Raw
	}
	return nil
}

func send(options Options, params gateway.SendParams) gateway.Response {
	params.Caller = options.Caller
	params.Workspace = workspace.ReadOrEmpty(options.WorkspaceDir, options.Caller)
	return gateway.Send(params)
}

// ExitCode maps a result onto the process exit status. Callers script against
// these, so the distinction between "the CLI failed" and "the build failed"
// has to survive into the shell.
func ExitCode(result Result) int {
	switch {
	case !result.OK:
		return ExitRequestFailed
	case result.TimedOut:
		return ExitTimedOut
	case result.Operation == nil, result.Operation.IsActive():
		return ExitOK
	case result.Operation.Succeeded():
		return ExitOK
	default:
		return ExitBuildFailed
	}
}

// Exit statuses.
const (
	ExitOK            = 0
	ExitRequestFailed = 1
	ExitBuildFailed   = 2
	ExitTimedOut      = 3
)
