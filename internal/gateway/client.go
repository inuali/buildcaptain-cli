package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// maxResponseBytes must fit a log response: the app caps log text at 2 MiB
// before JSON escaping, so this leaves ample room.
const maxResponseBytes = 8 * 1024 * 1024

// sun_path on macOS holds at most 104 bytes including the terminating NUL.
// The sandboxed app's socket path is longer, so long paths are dialed with a
// relative name from the socket's directory.
const maxSocketPathBytes = 103

// SendParams describes one gateway call.
type SendParams struct {
	Action          Action
	Caller          string
	Workspace       *WorkspaceContext
	ConfigurationID string
	Inputs          map[string]string
	OperationID     string
	Stage           string
	Lines           *int
}

// errNoResponse marks a connection the app accepted but closed without
// answering. That is how an app older than the request's action behaves, so it
// must not be mistaken for the app being absent.
var errNoResponse = errors.New("BuildCaptain closed the connection without answering.")

// Send performs a gateway round trip; if the socket is unavailable it
// launches BuildCaptain and retries for up to five seconds before giving up.
func Send(params SendParams) Response {
	request := Request{
		Version:         ProtocolVersion,
		ID:              NewUUID(),
		Action:          params.Action,
		Workspace:       params.Workspace,
		ConfigurationID: params.ConfigurationID,
		Inputs:          params.Inputs,
		OperationID:     params.OperationID,
		Stage:           params.Stage,
		Lines:           params.Lines,
	}

	response, err := roundTrip(request)
	if err == nil {
		return response
	}
	if errors.Is(err, errNoResponse) {
		return unansweredResponse(request)
	}
	launchBuildCaptain()
	for range 20 {
		time.Sleep(250 * time.Millisecond)
		if response, err = roundTrip(request); err == nil {
			return response
		}
		if errors.Is(err, errNoResponse) {
			return unansweredResponse(request)
		}
	}
	return failureResponse(request.ID, "BuildCaptain is not running or Agent Access is disabled.")
}

// unansweredResponse explains a silent close. The app drops requests it cannot
// decode, which for the newer actions means it predates them.
func unansweredResponse(request Request) Response {
	switch request.Action {
	case ActionStages, ActionLog:
		return failureResponse(request.ID,
			"The BuildCaptain app is too old to report stages and logs. Update it from the App Store.")
	default:
		return failureResponse(request.ID, "BuildCaptain did not answer the request.")
	}
}

func roundTrip(request Request) (Response, error) {
	conn, err := dialGateway()
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()

	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return Response{}, errors.New("Could not write to BuildCaptain.")
	}

	raw := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	for len(raw) <= maxResponseBytes {
		count, err := conn.Read(buffer)
		if count > 0 {
			raw = append(raw, buffer[:count]...)
		}
		if err != nil || bytes.IndexByte(raw, '\n') >= 0 {
			break
		}
	}
	newline := bytes.IndexByte(raw, '\n')
	if newline < 0 {
		if len(raw) == 0 {
			return Response{}, errNoResponse
		}
		return Response{}, errors.New("BuildCaptain did not return a complete response.")
	}
	var response Response
	if err := json.Unmarshal(raw[:newline], &response); err != nil {
		return Response{}, err
	}
	response.Raw = append(json.RawMessage(nil), raw[:newline]...)
	return response, nil
}

func dialGateway() (net.Conn, error) {
	var lastErr error = errors.New("BuildCaptain's local gateway is unavailable.")
	for _, path := range CandidateSocketPaths() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		conn, err := dialUnix(path)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// dialMu serializes the chdir window used for over-long socket paths so
// concurrent MCP tool calls cannot observe a foreign working directory.
var dialMu sync.Mutex

func dialUnix(path string) (net.Conn, error) {
	if len(path) <= maxSocketPathBytes {
		return net.DialTimeout("unix", path, 2*time.Second)
	}
	dialMu.Lock()
	defer dialMu.Unlock()
	previous, err := os.Open(".")
	if err != nil {
		return nil, err
	}
	defer previous.Close()
	if err := os.Chdir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	defer syscall.Fchdir(int(previous.Fd()))
	return net.DialTimeout("unix", filepath.Base(path), 2*time.Second)
}

func launchBuildCaptain() {
	// -g keeps the app in the background, matching the Swift helper's
	// NSWorkspace launch with activates=false.
	_ = exec.Command("/usr/bin/open", "-g", "-b", appBundleID).Run()
}
