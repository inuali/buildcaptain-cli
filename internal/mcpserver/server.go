// Package mcpserver exposes the BuildCaptain gateway as an MCP stdio server
// with the same identity and tools as the previous Swift helper.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/workspace"
)

const caller = "MCP agent"

type triggerInput struct {
	ConfigurationID string            `json:"configuration_id" jsonschema:"The ID returned by list_run_configurations"`
	Inputs          map[string]string `json:"inputs,omitempty" jsonschema:"Allowed parameter names and string values"`
}

type statusInput struct {
	OperationID string `json:"operation_id" jsonschema:"The operation ID returned by trigger_run"`
}

// Run serves MCP over stdio until the client disconnects. workspaceDir is the
// directory whose Git repository identifies the caller — captured at process
// start so the gateway dial's chdir can never affect it.
func Run(ctx context.Context, version, workspaceDir string) error {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "buildcaptain", Version: version},
		&mcp.ServerOptions{
			Instructions: "Use the listed safe run configurations to start Jenkins builds. Configurations restricted to a Git repository appear only when working in a matching checkout (override with BUILDCAPTAIN_REPO); configurations open to any repository are always available. Trigger returns an operation ID; poll get_run_status for progress.",
		},
	)

	send := func(params gateway.SendParams) (*mcp.CallToolResult, any, error) {
		params.Caller = caller
		params.Workspace = workspace.ReadOrEmpty(workspaceDir, caller)
		response := gateway.Send(params)
		text, err := gateway.FormatJSON(response)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
			IsError: !response.OK,
		}, nil, nil
	}

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "list_run_configurations",
			Description: "List the safe Jenkins run configurations available here: those allowed for the current Git repository plus those open to any repository.",
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input any) (*mcp.CallToolResult, any, error) {
			return send(gateway.SendParams{Action: gateway.ActionListConfigurations})
		},
	)

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "trigger_run",
			Description: "Start one allowed run configuration. This may wait for approval in BuildCaptain.",
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input triggerInput) (*mcp.CallToolResult, any, error) {
			if input.ConfigurationID == "" {
				return errorResult("configuration_id is required"), nil, nil
			}
			return send(gateway.SendParams{
				Action:          gateway.ActionTrigger,
				ConfigurationID: input.ConfigurationID,
				Inputs:          input.Inputs,
			})
		},
	)

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "get_run_status",
			Description: "Read the current state and result of an agent build operation.",
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
			operationID, ok := gateway.NormalizeUUID(input.OperationID)
			if !ok {
				return errorResult("A valid operation_id is required"), nil, nil
			}
			return send(gateway.SendParams{Action: gateway.ActionStatus, OperationID: operationID})
		},
	)

	return server.Run(ctx, &mcp.StdioTransport{})
}

func errorResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
		IsError: true,
	}
}
