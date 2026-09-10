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

// Run is what `run`, `wait`, and `status` print.
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
	// Stages lists the pipeline's stages with their own status, so a caller
	// sees where a build failed. Present once Jenkins has started the build.
	Stages []Stage `json:"stages,omitempty"`
	// StagesUnavailable explains a missing stage list for a started build,
	// such as an app too old to report one.
	StagesUnavailable string `json:"stages_unavailable,omitempty"`
	// Logs is the command that prints the whole build's console output.
	Logs     string `json:"logs,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
	// Poll and Wait are the literal commands that continue from here, present
	// only while the run can still change: Poll checks once and returns, Wait
	// blocks until there is an answer. Callers copy a command far more
	// reliably than they assemble one.
	Poll string          `json:"poll,omitempty"`
	Wait string          `json:"wait,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`
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

// Stage is one pipeline stage. Nested stages and parallel branches name
// their parent so the flat list still reads as a tree.
type Stage struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	StartedAt       string   `json:"started_at,omitempty"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
	Parent          string   `json:"parent,omitempty"`
	// Logs is the command that prints this stage's console output.
	Logs string `json:"logs"`
}

// Log is what `logs` prints.
type Log struct {
	OK    bool   `json:"ok"`
	RunID string `json:"run_id"`
	// Stage is set when the text belongs to one stage; absent for the
	// whole build's console.
	Stage *Stage `json:"stage,omitempty"`
	// Truncated says the start was cut, by --tail or by the app's size cap.
	Truncated bool            `json:"truncated"`
	Text      string          `json:"text"`
	Raw       json.RawMessage `json:"raw,omitempty"`
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

// NewRun projects a gateway operation onto the printed shape.
func NewRun(operation *gateway.Operation) Run {
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
	}
	// A finished build has no next step; offering one would invite a pointless
	// extra call.
	if operation.IsActive() {
		run.Poll = PollCommand(operation)
		run.Wait = ResumeCommand(operation)
	}
	if len(run.Inputs) == 0 {
		run.Inputs = nil
	}
	if jenkins := newJenkins(operation); jenkins != nil {
		run.Jenkins = jenkins
	}
	if operation.RunID != nil {
		run.Logs = LogsCommand(operation.ID, "")
	}
	return run
}

// NewStages projects the gateway's flattened stage graph.
func NewStages(operationID string, stages []gateway.Stage) []Stage {
	if len(stages) == 0 {
		return nil
	}
	list := make([]Stage, 0, len(stages))
	for _, stage := range stages {
		list = append(list, newStage(operationID, stage))
	}
	return list
}

func newStage(operationID string, stage gateway.Stage) Stage {
	return Stage{
		ID:              stage.ID,
		Name:            stage.Name,
		Status:          stage.Status,
		StartedAt:       stage.StartedAt.ISO8601(),
		DurationSeconds: stage.Duration,
		Parent:          stage.Parent,
		Logs:            LogsCommand(operationID, stage.Name),
	}
}

// NewLog projects a log response. stageQuery is what the caller asked for;
// the app resolves it, so the answer names the stage it actually matched.
func NewLog(operationID string, log *gateway.BuildLog) Log {
	result := Log{OK: true, RunID: runs.Format(operationID), Truncated: log.Truncated, Text: log.Text}
	if log.Stage != nil {
		stage := newStage(operationID, *log.Stage)
		result.Stage = &stage
	}
	return result
}

// LogsCommand prints a run's console output, or one stage's when named.
func LogsCommand(operationID, stage string) string {
	command := "buildcaptain logs " + runs.Format(operationID)
	if stage != "" {
		command += " --stage " + shellQuote(stage)
	}
	return command
}

// shellQuote wraps a value for copy-and-paste into a shell when it needs it;
// stage names routinely carry spaces.
func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n'\"\\$`!&|;<>()*?[]#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
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

// PollCommand checks this run once and returns.
func PollCommand(operation *gateway.Operation) string {
	return "buildcaptain status " + runs.Format(operation.ID)
}

// ResumeCommand blocks on this run until it finishes. It is what a caller
// reaches for after a wait was cut short.
func ResumeCommand(operation *gateway.Operation) string {
	return "buildcaptain wait " + runs.Format(operation.ID)
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
