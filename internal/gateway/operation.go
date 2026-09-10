package gateway

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// swiftReferenceEpoch is 2001-01-01T00:00:00Z in Unix seconds. App builds up
// to protocol 1.0 encoded dates with JSONEncoder's default strategy — seconds
// since that reference date — so a Unix-epoch reader is off by 31 years.
// Newer builds send ISO 8601 strings; Time accepts either.
const swiftReferenceEpoch = 978_307_200

// Time decodes both timestamp forms so one CLI works against any app version.
type Time struct{ time.Time }

func (t *Time) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == "" {
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("unusable timestamp %s: %w", text, err)
		}
		t.Time = parsed
		return nil
	}
	var seconds float64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return err
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return fmt.Errorf("unusable timestamp %s", text)
	}
	whole, fraction := math.Modf(seconds + swiftReferenceEpoch)
	t.Time = time.Unix(int64(whole), int64(fraction*float64(time.Second))).UTC()
	return nil
}

// ISO8601 renders the timestamp for CLI output, or "" when it is unset.
func (t Time) ISO8601() string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// OperationState values mirror AgentOperationState in the app.
const (
	StateAwaitingApproval = "awaitingApproval"
	StateSubmitting       = "submitting"
	StateQueued           = "queued"
	StateRunning          = "running"
	StateCompleted        = "completed"
	StateRejected         = "rejected"
	StateFailed           = "failed"
)

// Operation mirrors the fields of AgentBuildOperation the CLI reports on.
// Fields the app keeps for its own bookkeeping (configurationID,
// configurationRevision, resolvedParameters, expiresAt) are deliberately not
// modelled: relaying them only gave callers more IDs to confuse.
type Operation struct {
	ID                string            `json:"id"`
	ConfigurationKey  string            `json:"configurationKey"`
	ConfigurationName string            `json:"configurationName"`
	Repository        string            `json:"repository"`
	Branch            string            `json:"branch"`
	Commit            string            `json:"commit"`
	Caller            string            `json:"caller"`
	Inputs            map[string]string `json:"inputs"`
	RequestedAt       Time              `json:"requestedAt"`
	UpdatedAt         Time              `json:"updatedAt"`
	State             string            `json:"state"`
	Message           string            `json:"message"`
	QueueID           *int              `json:"queueID"`
	QueueURL          string            `json:"queueURL"`
	RunID             *JenkinsRun       `json:"runID"`
	Result            string            `json:"result"`
	ResolvedJob       *JenkinsJob       `json:"resolvedJob"`
}

// JenkinsRun mirrors RunID: the Jenkins-side build this operation produced.
type JenkinsRun struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// JenkinsJob mirrors the subset of JenkinsJob worth showing a caller.
type JenkinsJob struct {
	Name            string `json:"name"`
	FullDisplayName string `json:"fullDisplayName"`
}

// IsActive reports whether the operation can still change state.
func (o *Operation) IsActive() bool {
	switch o.State {
	case StateAwaitingApproval, StateSubmitting, StateQueued, StateRunning:
		return true
	default:
		return false
	}
}

// Succeeded reports whether a finished operation produced a good build. An
// operation that completed without a Jenkins result counts as successful:
// the app only reaches .completed after a build it considers done.
func (o *Operation) Succeeded() bool {
	return o.State == StateCompleted && (o.Result == "" || o.Result == "success")
}

// StateDescription renders the app's camelCase state as readable words.
func (o *Operation) StateDescription() string {
	var words strings.Builder
	for index, character := range o.State {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				words.WriteByte(' ')
			}
			words.WriteRune(character - 'A' + 'a')
			continue
		}
		words.WriteRune(character)
	}
	description := words.String()
	if o.Result != "" {
		description += " (" + strings.ReplaceAll(o.Result, "_", " ") + ")"
	}
	return description
}

// PublicConfiguration mirrors AgentPublicConfiguration.
type PublicConfiguration struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Approval string        `json:"approval"`
	Inputs   []PublicInput `json:"inputs"`
}

// PublicInput mirrors AgentPublicInput.
type PublicInput struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	AllowedValues []string `json:"allowedValues"`
	Pattern       *string  `json:"pattern"`
}

// Stage mirrors AgentPublicStage: one node of the flattened pipeline graph.
type Stage struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	StartedAt Time     `json:"startedAt"`
	Duration  *float64 `json:"duration"`
	Parent    string   `json:"parent"`
}

// BuildLog mirrors AgentBuildLog.
type BuildLog struct {
	Stage     *Stage `json:"stage"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}
