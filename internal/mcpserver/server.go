// Package mcpserver exposes the BuildCaptain gateway as an MCP stdio server.
// The tools mirror the command line exactly — same field names, same waiting
// behaviour, same result shape — so an agent that has read either surface
// already understands the other.
package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inuali/buildcaptain-cli/internal/output"
	"github.com/inuali/buildcaptain-cli/internal/runner"
)

const caller = "MCP agent"

// defaultWaitSeconds is shorter than the CLI's 30 minutes: MCP clients have
// their own request timeouts, and a wait that ends without an answer is worse
// than one that hands back a run ID to poll with.
const defaultWaitSeconds = 600

const instructions = `Use the listed safe run configurations to start Jenkins builds.

Configurations restricted to a Git repository appear only when working in a matching
checkout (override with BUILDCAPTAIN_REPO); configurations open to any repository are
always available.

trigger_run waits for the build to finish and reports the result. If it returns
"timed_out": true the build is still going — call get_run_status with the "run_id"
from the result. That run_id is the only identifier you ever pass back; the values
under "jenkins" are links for people, not handles for these tools.`

type triggerInput struct {
	Configuration string            `json:"configuration" jsonschema:"The configuration field from list_run_configurations"`
	Inputs        map[string]string `json:"inputs,omitempty" jsonschema:"Allowed input names and string values"`
	Wait          *bool             `json:"wait,omitempty" jsonschema:"Wait for the build to finish (default true)"`
	TimeoutSecs   int               `json:"timeout_seconds,omitempty" jsonschema:"How long to wait before returning a still-running build (default 600)"`

	// ConfigurationID is the pre-1.0 name for Configuration, accepted so
	// agents working from a cached tool description still succeed.
	ConfigurationID string `json:"configuration_id,omitempty" jsonschema:"Deprecated alias for configuration"`
}

type statusInput struct {
	// Optional: omitting it reports on the most recent run, which is what an
	// agent that lost track of the ID actually wants.
	RunID string `json:"run_id,omitempty" jsonschema:"The run_id returned by trigger_run; omit for the most recent run"`

	// OperationID is the pre-1.0 name for RunID, accepted for the same reason.
	OperationID string `json:"operation_id,omitempty" jsonschema:"Deprecated alias for run_id"`
}

// Run serves MCP over stdio until the client disconnects. workspaceDir is the
// directory whose Git repository identifies the caller — captured at process
// start so the gateway dial's chdir can never affect it.
func Run(ctx context.Context, version, workspaceDir string) error {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "buildcaptain", Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)

	options := runner.Options{WorkspaceDir: workspaceDir, Caller: caller}

	mcp.AddTool(server,
		&mcp.Tool{
			Name: "list_run_configurations",
			Description: "List the safe Jenkins run configurations available here: those allowed " +
				"for the current Git repository plus those open to any repository.",
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input any) (*mcp.CallToolResult, any, error) {
			return result(runner.Configs(options))
		},
	)

	mcp.AddTool(server,
		&mcp.Tool{
			Name: "trigger_run",
			Description: "Start one allowed run configuration and wait for it to finish. May wait " +
				"for approval in BuildCaptain. Returns the build's state, result, and a run_id.",
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input triggerInput) (*mcp.CallToolResult, any, error) {
			configuration := input.Configuration
			if configuration == "" {
				configuration = input.ConfigurationID
			}
			if configuration == "" {
				return result(runner.Result{
					Payload: output.NewFailure("configuration is required.",
						"Use the configuration field from list_run_configurations."),
				})
			}
			wait := true
			if input.Wait != nil {
				wait = *input.Wait
			}
			timeoutSeconds := input.TimeoutSecs
			if timeoutSeconds <= 0 {
				timeoutSeconds = defaultWaitSeconds
			}
			return result(runner.Trigger(ctx, runner.TriggerOptions{
				Options:       options,
				Configuration: configuration,
				Inputs:        input.Inputs,
				Wait:          wait,
				Timeout:       time.Duration(timeoutSeconds) * time.Second,
			}))
		},
	)

	mcp.AddTool(server,
		&mcp.Tool{
			Name: "get_run_status",
			Description: "Report the current state and result of a run started by trigger_run. " +
				"Omit run_id for the most recent run.",
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
			runID := input.RunID
			if runID == "" {
				runID = input.OperationID
			}
			return result(runner.Status(ctx, options, runID))
		},
	)

	return server.Run(ctx, &mcp.StdioTransport{})
}

// result renders a runner result as the tool's reply. A build that failed is
// reported as a successful tool call with a failed result: the tool did its
// job, and marking it an error invites agents to retry the call.
func result(value runner.Result) (*mcp.CallToolResult, any, error) {
	text, err := output.Encode(value.Payload)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: !value.OK,
	}, nil, nil
}
