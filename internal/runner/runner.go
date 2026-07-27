// Package runner performs the CLI's three real operations — list, trigger,
// status — and turns each into the printed shape. Both the command line and
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
	// caller able to say `buildcaptain status` with no argument.
	runs.Remember(operation.ID, operation.ConfigurationKey)

	timedOut, lostContact := false, false
	if options.Wait {
		if options.Progress != nil {
			options.Progress(operation)
		}
		operation, timedOut, lostContact, response = follow(ctx, options, operation, response)
	}
	if lostContact {
		// The build itself is unaffected, so say so and hand back the command
		// that picks the thread back up.
		return Result{
			Payload: output.NewFailure(
				"Lost contact with BuildCaptain while waiting. The build may still be running.",
				"Once the app is running again: "+output.PollCommand(operation)),
			OK: false,
		}
	}
	return runResult(operation, response, options.Verbose, timedOut)
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
	return runResult(response.Operation, response, options.Verbose, false)
}

// follow polls until the operation stops moving, the deadline passes, contact
// with the app is lost, or the context is cancelled. The last good response is
// kept so --verbose shows the wire form of the state actually reported.
func follow(
	ctx context.Context,
	options TriggerOptions,
	operation *gateway.Operation,
	response gateway.Response,
) (result *gateway.Operation, timedOut, lostContact bool, last gateway.Response) {
	if !operation.IsActive() {
		return operation, false, false, response
	}
	deadline := time.Time{}
	if options.Timeout > 0 {
		deadline = time.Now().Add(options.Timeout)
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

		polled := send(options.Options, gateway.SendParams{
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
				if options.Progress != nil {
					options.Progress(operation)
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
	verbose bool,
	timedOut bool,
) Result {
	// The poll hint belongs only where it is the caller's actual next step:
	// a run still in flight. Adding it to a finished build would invite a
	// pointless extra call.
	pollHint := ""
	if operation.IsActive() {
		pollHint = output.PollCommand(operation)
	}
	payload := output.NewRun(operation, pollHint)
	payload.TimedOut = timedOut
	if verbose {
		payload.Raw = rawOf(response)
	}
	return Result{Payload: payload, OK: true, TimedOut: timedOut, Operation: operation}
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
