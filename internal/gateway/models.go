// Package gateway implements the client side of BuildCaptain's local agent
// gateway: protocol version 1, newline-delimited JSON over a Unix socket.
// The wire format must stay compatible with AgentGatewayModels.swift in the
// BuildCaptain app.
package gateway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

const ProtocolVersion = 1

type Action string

const (
	ActionPing               Action = "ping"
	ActionListConfigurations Action = "listConfigurations"
	ActionTrigger            Action = "trigger"
	ActionStatus             Action = "status"
)

// WorkspaceContext mirrors AgentWorkspaceContext. Swift's synthesized Codable
// omits nil optionals, hence omitempty.
type WorkspaceContext struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Caller     string `json:"caller"`
}

// Request mirrors AgentGatewayRequest.
type Request struct {
	Version         int               `json:"version"`
	ID              string            `json:"id"`
	Action          Action            `json:"action"`
	Workspace       *WorkspaceContext `json:"workspace,omitempty"`
	ConfigurationID string            `json:"configurationID,omitempty"`
	Inputs          map[string]string `json:"inputs,omitempty"`
	OperationID     string            `json:"operationID,omitempty"`
}

// Response mirrors AgentGatewayResponse. Version and ID are transport
// concerns — the socket carries one request per connection, so nothing reads
// them — and never reach the CLI's own output; see the output package.
type Response struct {
	Version        int                   `json:"version"`
	ID             string                `json:"id"`
	OK             bool                  `json:"ok"`
	Error          string                `json:"error,omitempty"`
	Configurations []PublicConfiguration `json:"configurations,omitempty"`
	Operation      *Operation            `json:"operation,omitempty"`

	// Raw is the exact response line, kept for --verbose so the wire form
	// stays inspectable after the CLI has reshaped it.
	Raw json.RawMessage `json:"-"`
}

func failureResponse(requestID, message string) Response {
	return Response{Version: ProtocolVersion, ID: requestID, OK: false, Error: message}
}

// FormatRaw pretty-prints the response exactly as the app sent it, for
// --verbose. Responses the CLI synthesized itself have no wire form, so those
// fall back to re-encoding the struct.
func FormatRaw(response Response) (string, error) {
	if len(response.Raw) == 0 {
		return FormatJSON(response)
	}
	var generic any
	if err := json.Unmarshal(response.Raw, &generic); err != nil {
		return "", err
	}
	return marshalSortedIndent(generic)
}

// FormatJSON re-encodes a response pretty-printed with sorted keys.
func FormatJSON(response Response) (string, error) {
	compact, err := json.Marshal(response)
	if err != nil {
		return "", err
	}
	var generic map[string]any
	if err := json.Unmarshal(compact, &generic); err != nil {
		return "", err
	}
	pretty, err := marshalSortedIndent(generic)
	if err != nil {
		return "", err
	}
	return pretty, nil
}

func marshalSortedIndent(value any) (string, error) {
	// encoding/json sorts map keys and MarshalIndent handles indentation;
	// escaping of <, >, & is irrelevant for this payload but disabled to match
	// Swift's .withoutEscapingSlashes as closely as possible.
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// NormalizeUUID validates a UUID string case-insensitively (as Swift's
// UUID(uuidString:) does) and returns it in canonical uppercase form.
func NormalizeUUID(value string) (string, bool) {
	if len(value) != 36 {
		return "", false
	}
	upper := []byte(value)
	for i, c := range upper {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", false
			}
		default:
			switch {
			case c >= '0' && c <= '9', c >= 'A' && c <= 'F':
			case c >= 'a' && c <= 'f':
				upper[i] = c - 'a' + 'A'
			default:
				return "", false
			}
		}
	}
	return string(upper), true
}

// NewUUID returns a random (version 4) UUID in the uppercase-hex form Swift's
// UUID.uuidString produces.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on macOS
	}
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
