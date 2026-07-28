// buildcaptain is the command-line companion for the BuildCaptain macOS app.
// It talks to the app's local agent gateway and exposes it to AI coding
// agents as an MCP server.
//
// Output discipline: stdout carries exactly one JSON document per command —
// the result or the error, in one shape either way. Progress and usage go to
// stderr, so a caller can parse stdout without filtering it.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/mcpserver"
	"github.com/inuali/buildcaptain-cli/internal/output"
	"github.com/inuali/buildcaptain-cli/internal/runner"
	"github.com/inuali/buildcaptain-cli/internal/runs"
)

// version is stamped by the release build via -ldflags.
var version = "dev"

const cliCaller = "BuildCaptain CLI"

const usage = `BuildCaptain agent gateway

Usage:
  buildcaptain configs                       List the run configurations allowed here
  buildcaptain run <configuration> [flags]   Start a build and wait for it to finish
  buildcaptain wait [<run-id>]               Block on a running build until it finishes
  buildcaptain status [<run-id>]             Report on a run once and return
  buildcaptain mcp                           Serve the gateway to AI agents over MCP
  buildcaptain version

Flags for run:
  --input NAME=VALUE   Supply one allowed input; repeat for more
  --no-wait            Return as soon as the build is accepted, with a run ID to poll

Flags for run and wait:
  --timeout DURATION   Give up waiting after this long (default 30m, 0 waits forever)
  --quiet              Do not print progress to stderr

Flags for every command:
  --verbose            Include the raw gateway response under "raw"

Run IDs look like run_1a2b3c4d…; every command that touches a run prints one.
Any unambiguous prefix of at least 6 characters works: buildcaptain wait run_1a2b3c.
Omitting the run ID uses the most recent run started from this Mac — convenient,
but ambiguous when several are in flight, so keep the ID when it matters.

Exit codes:
  0  the build succeeded, or --no-wait accepted it
  1  the request failed (not allowed, app unreachable, bad arguments)
  2  the build finished unsuccessfully, or was rejected
  3  the wait timed out; the build is still running
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	workspaceDir, err := os.Getwd()
	if err != nil {
		return fail("Could not determine the working directory: "+err.Error(), "")
	}

	command := ""
	var rest []string
	if len(arguments) > 0 {
		command, rest = arguments[0], arguments[1:]
	}

	switch command {
	case "mcp":
		if err := mcpserver.Run(context.Background(), version, workspaceDir); err != nil {
			return fail(err.Error(), "")
		}
		return runner.ExitOK

	case "configs":
		flags, _, err := parseFlags(rest, policyReading)
		if err != nil {
			return fail(err.Error(), "")
		}
		return report(runner.Configs(runner.Options{
			WorkspaceDir: workspaceDir,
			Caller:       cliCaller,
			Verbose:      flags.verbose,
		}))

	case "run":
		flags, positional, err := parseFlags(rest, policyRun)
		if err != nil {
			return fail(err.Error(), "")
		}
		if len(positional) != 1 {
			return fail("Usage: buildcaptain run <configuration> [--input NAME=VALUE]",
				"Run buildcaptain configs to see the configurations allowed here.")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return report(runner.Trigger(ctx, runner.TriggerOptions{
			Options: runner.Options{
				WorkspaceDir: workspaceDir,
				Caller:       cliCaller,
				Verbose:      flags.verbose,
			},
			Configuration: positional[0],
			Inputs:        flags.inputs,
			Wait:          !flags.noWait,
			Timeout:       flags.timeout,
			Progress:      progressPrinter(flags.quiet),
			Accepted:      acceptancePrinter(flags.quiet || flags.noWait),
		}))

	case "wait":
		flags, positional, err := parseFlags(rest, policyWait)
		if err != nil {
			return fail(err.Error(), "")
		}
		if len(positional) > 1 {
			return fail("Usage: buildcaptain wait [<run-id>] [--timeout DURATION]", "")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return report(runner.Wait(ctx, runner.WaitOptions{
			Options: runner.Options{
				WorkspaceDir: workspaceDir,
				Caller:       cliCaller,
				Verbose:      flags.verbose,
			},
			RunID:    first(positional),
			Timeout:  flags.timeout,
			Progress: progressPrinter(flags.quiet),
		}))

	case "status":
		flags, positional, err := parseFlags(rest, policyReading)
		if err != nil {
			return fail(err.Error(), "")
		}
		if len(positional) > 1 {
			return fail("Usage: buildcaptain status [<run-id>]", "")
		}
		return report(runner.Status(context.Background(), runner.Options{
			WorkspaceDir: workspaceDir,
			Caller:       cliCaller,
			Verbose:      flags.verbose,
		}, first(positional)))

	case "version":
		fmt.Println(version)
		return runner.ExitOK

	default:
		fmt.Fprint(os.Stderr, usage)
		if command != "" && command != "help" && command != "--help" && command != "-h" {
			return fail(fmt.Sprintf("%q is not a buildcaptain command.", command), "")
		}
		return runner.ExitOK
	}
}

type flagSet struct {
	inputs  map[string]string
	noWait  bool
	timeout time.Duration
	quiet   bool
	verbose bool
}

// flagPolicy says which of the CLI's flags a command accepts, so a flag used
// on the wrong command is refused by name instead of being ignored.
type flagPolicy struct {
	// starting covers the flags that only make sense when beginning a build.
	starting bool
	// waiting covers the flags that shape how a command follows a build.
	waiting bool
}

var (
	policyRun     = flagPolicy{starting: true, waiting: true}
	policyWait    = flagPolicy{waiting: true}
	policyReading = flagPolicy{}
)

// parseFlags reads the CLI's small, fixed flag vocabulary.
func parseFlags(arguments []string, policy flagPolicy) (flagSet, []string, error) {
	flags := flagSet{inputs: map[string]string{}, timeout: runner.DefaultTimeout}
	var positional []string

	refuse := func(flag string, allowed bool) error {
		if allowed {
			return nil
		}
		if flag == "--input" || flag == "--no-wait" {
			return fmt.Errorf("%s only applies to buildcaptain run.", flag)
		}
		return fmt.Errorf("%s only applies to buildcaptain run and buildcaptain wait.", flag)
	}

	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		// Both --flag value and --flag=value are accepted; next() advances the
		// loop index only in the first form.
		inlineValue := ""
		hasInlineValue := false
		if name, inline, found := strings.Cut(argument, "="); found && strings.HasPrefix(argument, "--") {
			argument, inlineValue, hasInlineValue = name, inline, true
		}
		next := func(name string) (string, error) {
			if hasInlineValue {
				return inlineValue, nil
			}
			if index+1 >= len(arguments) {
				return "", fmt.Errorf("%s needs a value.", name)
			}
			index++
			return arguments[index], nil
		}

		switch argument {
		case "--verbose":
			flags.verbose = true
		case "--quiet":
			if err := refuse(argument, policy.waiting); err != nil {
				return flags, nil, err
			}
			flags.quiet = true
		case "--no-wait":
			if err := refuse(argument, policy.starting); err != nil {
				return flags, nil, err
			}
			flags.noWait = true
		case "--timeout":
			if err := refuse(argument, policy.waiting); err != nil {
				return flags, nil, err
			}
			raw, err := next("--timeout")
			if err != nil {
				return flags, nil, err
			}
			duration, err := time.ParseDuration(raw)
			if err != nil || duration < 0 {
				return flags, nil, fmt.Errorf("--timeout takes a duration such as 90s, 10m, or 0 to wait forever; got %q.", raw)
			}
			flags.timeout = duration
		case "--input":
			if err := refuse(argument, policy.starting); err != nil {
				return flags, nil, err
			}
			raw, err := next("--input")
			if err != nil {
				return flags, nil, err
			}
			name, inputValue, found := strings.Cut(raw, "=")
			if !found || name == "" {
				return flags, nil, fmt.Errorf("Inputs use --input NAME=VALUE; got %q.", raw)
			}
			flags.inputs[name] = inputValue
		default:
			if strings.HasPrefix(argument, "-") {
				return flags, nil, fmt.Errorf("%q is not a known flag.", argument)
			}
			positional = append(positional, argument)
		}
	}
	return flags, positional, nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// acceptancePrinter announces the run ID on stderr the moment BuildCaptain
// takes the build, before any waiting. A caller whose process is killed
// mid-wait — a harness with a short command window, say — has then already
// been told the ID and the command that resumes from it, instead of losing
// track of a build that is still running.
func acceptancePrinter(quiet bool) func(*gateway.Operation) {
	if quiet {
		return nil
	}
	return func(operation *gateway.Operation) {
		fmt.Fprintf(os.Stderr, "%s: started %s — resume with: %s\n",
			operation.ConfigurationKey, runs.Format(operation.ID), output.ResumeCommand(operation))
	}
}

// progressPrinter reports each state change on stderr, so a caller watching
// the terminal sees the build move while stdout stays a single document.
func progressPrinter(quiet bool) func(*gateway.Operation) {
	if quiet {
		return nil
	}
	return func(operation *gateway.Operation) {
		label := operation.ConfigurationKey
		if operation.RunID != nil {
			label += fmt.Sprintf(" #%d", operation.RunID.Number)
		}
		fmt.Fprintln(os.Stderr, label+": "+operation.StateDescription())
	}
}

func report(result runner.Result) int {
	text, err := output.Encode(result.Payload)
	if err != nil {
		return fail("Could not render the result: "+err.Error(), "")
	}
	fmt.Println(text)
	return runner.ExitCode(result)
}

// fail prints an error in the same shape as a successful result, so callers
// parse one document either way.
func fail(message, hint string) int {
	text, err := output.Encode(output.NewFailure(message, hint))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: "+message)
		return runner.ExitRequestFailed
	}
	fmt.Println(text)
	return runner.ExitRequestFailed
}
