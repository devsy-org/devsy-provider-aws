package cmd

import (
	"strings"
	"testing"
)

const (
	infoLevel  = "info"
	jsonFormat = "json"
)

func TestLoggingConfig(t *testing.T) {
	for _, level := range []string{"", "error", "warn", infoLevel, "debug", "trace", "TrAcE"} {
		for _, format := range []string{"", "auto", jsonFormat, "text", "plain", "logfmt", "PlAiN"} {
			assertLoggingConfig(t, level, format)
		}
	}
}

func TestLoggingConfigRejectsInvalidValuesWithoutEcho(t *testing.T) {
	const secret = "secret-value-canary"
	for _, values := range [][2]string{
		{secret, jsonFormat},
		{infoLevel, secret},
		{" info", jsonFormat},
		{infoLevel, "json "},
	} {
		_, err := loggingConfig(values[0], values[1])
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("invalid configuration error: %v", err)
		}
	}
}

func TestInvalidLoggingConfigStopsStatusCommand(t *testing.T) {
	const secret = "secret-value-canary"
	t.Setenv("DEVSY_LOG_LEVEL", secret)
	cmd := BuildRoot()
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "DEVSY_LOG_LEVEL") ||
		strings.Contains(err.Error(), secret) {
		t.Fatalf("unexpected command error: %v", err)
	}
}

func assertLoggingConfig(t *testing.T, level, format string) {
	t.Helper()
	cfg, err := loggingConfig(level, format)
	if err != nil {
		t.Fatalf("level %q format %q: %v", level, format, err)
	}
	wantFormat := strings.ToLower(format)
	switch wantFormat {
	case "", "auto":
		wantFormat = jsonFormat
	case "plain":
		wantFormat = "text"
	}
	if cfg.Level != strings.ToLower(level) || cfg.DefaultLevel != infoLevel ||
		cfg.Format != wantFormat {
		t.Fatalf("unexpected configuration: %#v", cfg)
	}
}
