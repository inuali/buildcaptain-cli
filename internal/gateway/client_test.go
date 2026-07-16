package gateway

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serve accepts connections and answers every request with ok:true, echoing
// the request id — the framing contract of AgentGatewayServer.swift.
func serve(t *testing.T, listener net.Listener) {
	t.Helper()
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
				var request Request
				if err := json.Unmarshal(line, &request); err != nil {
					return
				}
				response, _ := json.Marshal(Response{Version: ProtocolVersion, ID: request.ID, OK: true})
				conn.Write(append(response, '\n'))
			}()
		}
	}()
}

func listenAt(t *testing.T, path string) net.Listener {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// The test server needs the same long-path workaround as the client.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	listener, err := net.Listen("unix", filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func sendOverSocket(t *testing.T, socketPath string) Response {
	t.Helper()
	serve(t, listenAt(t, socketPath))
	t.Setenv("BUILDCAPTAIN_GATEWAY_SOCKET", socketPath)
	return Send(SendParams{
		Action:    ActionListConfigurations,
		Caller:    "test",
		Workspace: &WorkspaceContext{Repository: "github.com/acme/widgets", Caller: "test"},
	})
}

func TestSendShortSocketPath(t *testing.T) {
	response := sendOverSocket(t, filepath.Join(t.TempDir(), "gateway.sock"))
	if !response.OK {
		t.Errorf("expected ok response, got %+v", response)
	}
}

// The sandboxed app's socket path exceeds the 104-byte sun_path limit; the
// client must still reach it via a relative dial.
func TestSendLongSocketPath(t *testing.T) {
	base := t.TempDir()
	long := filepath.Join(base, strings.Repeat("d", 60), strings.Repeat("e", 60))
	socketPath := filepath.Join(long, "gateway.sock")
	if len(socketPath) <= maxSocketPathBytes {
		t.Fatalf("test setup: path %q not long enough", socketPath)
	}
	response := sendOverSocket(t, socketPath)
	if !response.OK {
		t.Errorf("expected ok response over long path, got %+v", response)
	}
}

