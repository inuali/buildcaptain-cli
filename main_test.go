package main

import (
	"strings"
	"testing"
	"time"

	"github.com/inuali/buildcaptain-cli/internal/runner"
)

// Running with no arguments must print usage, not crash.
func TestNoArgumentsPrintsUsage(t *testing.T) {
	if code := run(nil); code != runner.ExitOK {
		t.Errorf("run(nil) = %d, want %d", code, runner.ExitOK)
	}
	if code := run([]string{}); code != runner.ExitOK {
		t.Errorf("run([]) = %d, want %d", code, runner.ExitOK)
	}
}

func TestParseRunFlags(t *testing.T) {
	flags, positional, err := parseFlags([]string{
		"deploy-staging", "--input", "TARGET=staging", "--input=branch=release/1.4",
		"--no-wait", "--timeout", "90s", "--quiet", "--verbose",
	}, policyRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(positional) != 1 || positional[0] != "deploy-staging" {
		t.Errorf("positional = %v", positional)
	}
	if flags.inputs["TARGET"] != "staging" || flags.inputs["branch"] != "release/1.4" {
		t.Errorf("inputs = %v", flags.inputs)
	}
	if !flags.noWait || !flags.quiet || !flags.verbose {
		t.Errorf("boolean flags not set: %+v", flags)
	}
	if flags.timeout != 90*time.Second {
		t.Errorf("timeout = %s", flags.timeout)
	}
}

func TestWaitingIsTheDefault(t *testing.T) {
	flags, _, err := parseFlags([]string{"deploy-staging"}, policyRun)
	if err != nil {
		t.Fatal(err)
	}
	if flags.noWait {
		t.Error("run should wait unless --no-wait is given")
	}
	if flags.timeout != runner.DefaultTimeout {
		t.Errorf("timeout = %s, want the %s default", flags.timeout, runner.DefaultTimeout)
	}
}

// A value containing "=" must survive; deploy inputs routinely carry them.
func TestInputValuesKeepEqualsSigns(t *testing.T) {
	flags, _, err := parseFlags([]string{"--input", "ARGS=--flag=1 --other=2"}, policyRun)
	if err != nil {
		t.Fatal(err)
	}
	if flags.inputs["ARGS"] != "--flag=1 --other=2" {
		t.Errorf("ARGS = %q", flags.inputs["ARGS"])
	}
}

func TestFlagsAreRefusedOnCommandsThatIgnoreThem(t *testing.T) {
	for _, flag := range []string{"--no-wait", "--quiet", "--timeout", "--input"} {
		if _, _, err := parseFlags([]string{flag, "x"}, policyReading); err == nil {
			t.Errorf("%s should be rejected on configs and status", flag)
		}
	}
	// wait follows a build, so it takes the waiting flags but not the ones
	// that only mean something when starting one.
	for _, arguments := range [][]string{{"--timeout", "90s"}, {"--quiet"}} {
		if _, _, err := parseFlags(arguments, policyWait); err != nil {
			t.Errorf("wait should accept %v: %v", arguments, err)
		}
	}
	for _, arguments := range [][]string{{"--no-wait"}, {"--input", "A=B"}} {
		_, _, err := parseFlags(arguments, policyWait)
		if err == nil {
			t.Errorf("wait should reject %v", arguments)
			continue
		}
		if !strings.Contains(err.Error(), "only applies to buildcaptain run.") {
			t.Errorf("error for %v should name the right command, got %q", arguments, err)
		}
	}
	// --verbose applies everywhere.
	if _, _, err := parseFlags([]string{"--verbose"}, policyReading); err != nil {
		t.Errorf("--verbose should be accepted anywhere: %v", err)
	}
}

func TestParseLogsFlags(t *testing.T) {
	flags, positional, err := parseFlags([]string{"run_1a2b3c", "--stage", "Unit tests", "--tail=50"}, policyLogs)
	if err != nil {
		t.Fatal(err)
	}
	if len(positional) != 1 || positional[0] != "run_1a2b3c" {
		t.Errorf("positional = %v", positional)
	}
	if flags.stage != "Unit tests" || flags.tail != 50 {
		t.Errorf("flags = %+v", flags)
	}
	for _, arguments := range [][]string{{"--tail", "0"}, {"--tail", "many"}, {"--stage"}} {
		if _, _, err := parseFlags(arguments, policyLogs); err == nil {
			t.Errorf("parseFlags(%v) should have failed", arguments)
		}
	}
	// The log flags mean nothing on the other commands, and say so.
	for _, policy := range []flagPolicy{policyRun, policyWait, policyReading} {
		_, _, err := parseFlags([]string{"--stage", "Build"}, policy)
		if err == nil || !strings.Contains(err.Error(), "only applies to buildcaptain logs.") {
			t.Errorf("--stage under %+v: %v", policy, err)
		}
	}
}

func TestFlagErrorsAreSpecific(t *testing.T) {
	cases := [][]string{
		{"--bogus"},
		{"--input", "NOEQUALS"},
		{"--input", "=novalue"},
		{"--timeout", "soon"},
		{"--timeout"},
		{"--input"},
	}
	for _, arguments := range cases {
		if _, _, err := parseFlags(arguments, policyRun); err == nil {
			t.Errorf("parseFlags(%v) should have failed", arguments)
		}
	}
}

// A run URL is a run reference like any run ID, never mistaken for a flag.
func TestRunURLIsThePositionalRunReference(t *testing.T) {
	const runURL = "https://jenkins.example.com/job/jobfoto-teams/job/main/481/"
	for _, policy := range []flagPolicy{policyReading, policyWait, policyLogs} {
		_, positional, err := parseFlags([]string{runURL, "--verbose"}, policy)
		if err != nil || len(positional) != 1 || positional[0] != runURL {
			t.Errorf("positional = %v, err = %v", positional, err)
		}
	}
}
