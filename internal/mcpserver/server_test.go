package mcpserver

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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
)

const runURL = "https://jenkins.example.com/job/jobfoto-teams/job/main/481/"

// fakeApp answers run-URL requests the way BuildCaptain does, recording what
// it was sent.
type fakeApp struct {
	mutex    sync.Mutex
	requests []gateway.Request
	// legacy answers like an app that predates run URLs.
	legacy bool
}

func (f *fakeApp) handle(request gateway.Request) gateway.Response {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.requests = append(f.requests, request)
	response := gateway.Response{Version: gateway.ProtocolVersion, ID: request.ID, OK: true}
	if f.legacy || request.RunURL == "" {
		return gateway.Response{Version: gateway.ProtocolVersion, ID: request.ID, Error: "Agent build operation not found."}
	}
	switch request.Action {
	case gateway.ActionStatus:
		response.Run = &gateway.RunReport{
			URL: runURL, Number: 481, Job: "jobfoto-teams » main",
			ConfigurationKey: "jobfoto-deploy", ConfigurationName: "Jobfoto deploy",
			State: gateway.StateCompleted, Result: "success",
		}
	case gateway.ActionStages:
		response.Stages = []gateway.Stage{{ID: "6", Name: "Deploy", Status: "success"}}
	case gateway.ActionLog:
		response.Log = &gateway.BuildLog{Text: "deployed\n"}
	}
	return response
}

func (f *fakeApp) sent() []gateway.Request {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]gateway.Request(nil), f.requests...)
}

func (f *fakeApp) start(t *testing.T) string {
	t.Helper()
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
	t.Setenv("BUILDCAPTAIN_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	t.Setenv("BUILDCAPTAIN_REPO", "")
	return t.TempDir()
}

func connect(t *testing.T, workspaceDir string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := newServer("test", workspaceDir).Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, tool string, arguments map[string]any) (map[string]any, bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content = %#v", tool, result.Content)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		t.Fatalf("%s: %v in %s", tool, err, text.Text)
	}
	return payload, result.IsError
}

// The run URL BuildCaptain's copy button hands over works in run_id, for a
// single read, for a blocking wait, and for logs.
func TestRunToolsAcceptARunURL(t *testing.T) {
	app := &fakeApp{}
	session := connect(t, app.start(t))

	status, isError := call(t, session, "get_run_status", map[string]any{"run_id": runURL})
	if isError || status["run_id"] != runURL || status["state"] != "completed" || status["result"] != "success" {
		t.Errorf("get_run_status = %v (error %v)", status, isError)
	}
	waited, isError := call(t, session, "get_run_status", map[string]any{"run_id": runURL, "wait_seconds": 5})
	if isError || waited["run_id"] != runURL || waited["state"] != "completed" {
		t.Errorf("get_run_status waiting = %v (error %v)", waited, isError)
	}
	logs, isError := call(t, session, "get_run_logs", map[string]any{"run_id": runURL, "stage": "Deploy"})
	if isError || logs["run_id"] != runURL || logs["text"] != "deployed\n" {
		t.Errorf("get_run_logs = %v (error %v)", logs, isError)
	}

	for _, request := range app.sent() {
		if request.RunURL != runURL || request.OperationID != "" {
			t.Errorf("%s sent operationID %q, runURL %q", request.Action, request.OperationID, request.RunURL)
		}
		if request.Action == gateway.ActionLog && (request.Stage != "Deploy" || request.Lines == nil || *request.Lines != 200) {
			t.Errorf("log request = %+v", request)
		}
	}
}

func TestRunToolsExplainAnAppTooOldForRunURLs(t *testing.T) {
	app := &fakeApp{legacy: true}
	session := connect(t, app.start(t))

	for _, tool := range []string{"get_run_status", "get_run_logs"} {
		payload, isError := call(t, session, tool, map[string]any{"run_id": runURL})
		message, _ := payload["error"].(string)
		if !isError || !strings.Contains(message, "needs a newer BuildCaptain") {
			t.Errorf("%s = %v (error %v)", tool, payload, isError)
		}
	}
}
