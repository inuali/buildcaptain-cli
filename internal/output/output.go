// Package output defines what the CLI prints. This is the CLI's contract with
// its callers — deliberately not the gateway's wire format, which carries
// transport fields and internal identifiers that only gave callers more IDs to
// mistake for the one that matters.
//
// Field order is meaningful: struct order is preserved by encoding/json, and
// run_id comes first so the ID a caller needs is the first thing read.
package output

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/inuali/buildcaptain-cli/internal/gateway"
	"github.com/inuali/buildcaptain-cli/internal/runs"
)

// Run is what `run` and `status` print.
type Run struct {
	OK            bool              `json:"ok"`
	RunID         string            `json:"run_id"`
	State         string            `json:"state"`
	Result        string            `json:"result,omitempty"`
	Configuration string            `json:"configuration"`
	Name          string            `json:"name,omitempty"`
	Message       string            `json:"message,omitempty"`
	Branch        string            `json:"branch,omitempty"`
	Inputs        map[string]string `json:"inputs,omitempty"`
	RequestedAt   string            `json:"requested_at,omitempty"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
	Jenkins       *Jenkins          `json:"jenkins,omitempty"`
	TimedOut      bool              `json:"timed_out,omitempty"`
	Poll          string            `json:"poll,omitempty"`
	Raw           json.RawMessage   `json:"raw,omitempty"`
}

// Jenkins groups the Jenkins-side identifiers. They are nested and named for
// what they link to so they cannot be mistaken for the run ID to poll with.
type Jenkins struct {
	Job         string `json:"job,omitempty"`
	QueueID     *int   `json:"queue_id,omitempty"`
	QueueURL    string `json:"queue_url,omitempty"`
	BuildNumber *int   `json:"build_number,omitempty"`
	BuildURL    string `json:"build_url,omitempty"`
}

// Configs is what `configs` prints.
type Configs struct {
	OK             bool            `json:"ok"`
	Configurations []Configuration `json:"configurations"`
	Raw            json.RawMessage `json:"raw,omitempty"`
}

// Configuration names its key `configuration` — the same field name `run`
// takes and echoes back, so one term follows a build end to end.
type Configuration struct {
	Configuration string  `json:"configuration"`
	Name          string  `json:"name"`
	Approval      string  `json:"approval"`
	Inputs        []Input `json:"inputs,omitempty"`
	Run           string  `json:"run"`
}

// Input describes one value the caller may supply.
type Input struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	AllowedValues []string `json:"allowed_values,omitempty"`
	Pattern       string   `json:"pattern,omitempty"`
}

// Failure is what every error prints, so callers can parse one shape.
type Failure struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Hint  string          `json:"hint,omitempty"`
	Raw   json.RawMessage `json:"raw,omitempty"`
}

// NewFailure builds an error payload.
func NewFailure(message, hint string) Failure {
	return Failure{OK: false, Error: message, Hint: hint}
}

// NewRun projects a gateway operation onto the printed shape. pollHint is the
// command to poll with, included only when the run is still moving.
func NewRun(operation *gateway.Operation, pollHint string) Run {
	run := Run{
		OK:            true,
		RunID:         runs.Format(operation.ID),
		State:         operation.State,
		Result:        operation.Result,
		Configuration: operation.ConfigurationKey,
		Name:          operation.ConfigurationName,
		Message:       operation.Message,
		Branch:        operation.Branch,
		Inputs:        operation.Inputs,
		RequestedAt:   operation.RequestedAt.ISO8601(),
		UpdatedAt:     operation.UpdatedAt.ISO8601(),
		Poll:          pollHint,
	}
	if len(run.Inputs) == 0 {
		run.Inputs = nil
	}
	if jenkins := newJenkins(operation); jenkins != nil {
		run.Jenkins = jenkins
	}
	return run
}

func newJenkins(operation *gateway.Operation) *Jenkins {
	jenkins := Jenkins{QueueID: operation.QueueID, QueueURL: operation.QueueURL}
	if job := operation.ResolvedJob; job != nil {
		jenkins.Job = job.FullDisplayName
		if jenkins.Job == "" {
			jenkins.Job = job.Name
		}
	}
	if build := operation.RunID; build != nil {
		number := build.Number
		jenkins.BuildNumber = &number
		jenkins.BuildURL = build.URL
	}
	if jenkins == (Jenkins{}) {
		return nil
	}
	return &jenkins
}

// PollCommand is the literal command that reports on this run. Callers follow
// a command they can copy far more reliably than one they must assemble.
func PollCommand(operation *gateway.Operation) string {
	return "buildcaptain status " + runs.Format(operation.ID)
}

// NewConfigs projects the gateway's configuration list, giving each entry the
// exact command that starts it.
func NewConfigs(configurations []gateway.PublicConfiguration) Configs {
	list := make([]Configuration, 0, len(configurations))
	for _, configuration := range configurations {
		entry := Configuration{
			Configuration: configuration.ID,
			Name:          configuration.Name,
			Approval:      configuration.Approval,
		}
		command := strings.Builder{}
		command.WriteString("buildcaptain run " + configuration.ID)
		for _, input := range configuration.Inputs {
			value := ""
			if input.Pattern != nil {
				value = *input.Pattern
			}
			entry.Inputs = append(entry.Inputs, Input{
				Name:          input.Name,
				Type:          input.Type,
				AllowedValues: input.AllowedValues,
				Pattern:       value,
			})
			command.WriteString(" --input " + input.Name + "=<value>")
		}
		entry.Run = command.String()
		list = append(list, entry)
	}
	return Configs{OK: true, Configurations: list}
}

// Encode renders a payload as the single JSON document a command prints.
func Encode(payload any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		return "", err
	}
	return strings.TrimRight(buffer.String(), "\n"), nil
}
