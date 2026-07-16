// buildcaptain is the command-line companion for the BuildCaptain macOS app.
// It talks to the app's local agent gateway and exposes it to AI coding
// agents as an MCP server.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/mcpserver"
	"github.com/inuali/buildcaptain-cli/internal/workspace"
)

// version is stamped by the release build via -ldflags.
var version = "dev"

const cliCaller = "BuildCaptain CLI"

const usage = `BuildCaptain agent gateway

Usage:
  buildcaptain mcp
  buildcaptain configs
  buildcaptain run <configuration> [--input NAME=VALUE]
  buildcaptain status <operation-id>
  buildcaptain version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	workspaceDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("could not determine the working directory: %w", err)
	}

	command := ""
	if len(arguments) > 0 {
		command = arguments[0]
	}
	switch command {
	case "mcp":
		return mcpserver.Run(context.Background(), version, workspaceDir)
	case "configs":
		return printResponse(send(workspaceDir, gateway.SendParams{Action: gateway.ActionListConfigurations}))
	case "run":
		if len(arguments) < 2 {
			return fmt.Errorf("Usage: buildcaptain run <configuration> [--input NAME=VALUE]")
		}
		inputs := map[string]string{}
		for index := 2; index < len(arguments); index += 2 {
			if arguments[index] != "--input" || index+1 >= len(arguments) || !strings.Contains(arguments[index+1], "=") {
				return fmt.Errorf("Inputs use --input NAME=VALUE.")
			}
			name, value, _ := strings.Cut(arguments[index+1], "=")
			inputs[name] = value
		}
		return printResponse(send(workspaceDir, gateway.SendParams{
			Action:          gateway.ActionTrigger,
			ConfigurationID: arguments[1],
			Inputs:          inputs,
		}))
	case "status":
		if len(arguments) != 2 {
			return fmt.Errorf("Usage: buildcaptain status <operation-id>")
		}
		operationID, ok := gateway.NormalizeUUID(arguments[1])
		if !ok {
			return fmt.Errorf("Usage: buildcaptain status <operation-id>")
		}
		return printResponse(send(workspaceDir, gateway.SendParams{Action: gateway.ActionStatus, OperationID: operationID}))
	case "version":
		fmt.Println(version)
		return nil
	default:
		fmt.Print(usage)
		return nil
	}
}

func send(workspaceDir string, params gateway.SendParams) gateway.Response {
	params.Caller = cliCaller
	params.Workspace = workspace.ReadOrEmpty(workspaceDir, cliCaller)
	return gateway.Send(params)
}

func printResponse(response gateway.Response) error {
	text, err := gateway.FormatJSON(response)
	if err != nil {
		return err
	}
	fmt.Println(text)
	if !response.OK {
		return fmt.Errorf("BuildCaptain rejected the request.")
	}
	return nil
}
