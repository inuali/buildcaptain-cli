package main

import (
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
	}, true)
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
	flags, _, err := parseFlags([]string{"deploy-staging"}, true)
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
	flags, _, err := parseFlags([]string{"--input", "ARGS=--flag=1 --other=2"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if flags.inputs["ARGS"] != "--flag=1 --other=2" {
		t.Errorf("ARGS = %q", flags.inputs["ARGS"])
	}
}

func TestRunOnlyFlagsRejectedElsewhere(t *testing.T) {
	for _, flag := range []string{"--no-wait", "--quiet", "--timeout", "--input"} {
		if _, _, err := parseFlags([]string{flag, "x"}, false); err == nil {
			t.Errorf("%s should be rejected outside run", flag)
		}
	}
	// --verbose applies everywhere.
	if _, _, err := parseFlags([]string{"--verbose"}, false); err != nil {
		t.Errorf("--verbose should be accepted anywhere: %v", err)
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
		if _, _, err := parseFlags(arguments, true); err == nil {
			t.Errorf("parseFlags(%v) should have failed", arguments)
		}
	}
}
