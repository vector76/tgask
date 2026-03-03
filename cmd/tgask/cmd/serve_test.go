package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "serve"}
	cmd.Flags().String("token", "", "HTTP bearer token (overrides TGASK_TOKEN)")
	return cmd
}

func TestServeConfigTokenFlagOverridesEnv(t *testing.T) {
	t.Setenv("TGASK_TOKEN", "env-tok")
	t.Setenv("TGASK_BOT_TOKEN", "bot-token")
	t.Setenv("TGASK_CHAT_ID", "12345")
	t.Setenv("TGASK_PORT", "8080")

	cmd := newServeCmd()
	if err := cmd.Flags().Set("token", "flag-tok"); err != nil {
		t.Fatalf("setting --token flag: %v", err)
	}

	cfg, err := resolveServeConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.token != "flag-tok" {
		t.Errorf("expected token %q, got %q", "flag-tok", cfg.token)
	}
}

func TestServeConfigEmptyTokenFlagFallsBackToEnv(t *testing.T) {
	t.Setenv("TGASK_TOKEN", "env-tok")
	t.Setenv("TGASK_BOT_TOKEN", "bot-token")
	t.Setenv("TGASK_CHAT_ID", "12345")
	t.Setenv("TGASK_PORT", "8080")

	cmd := newServeCmd()

	cfg, err := resolveServeConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.token != "env-tok" {
		t.Errorf("expected token %q, got %q", "env-tok", cfg.token)
	}
}

func TestServeConfigJobTimeoutFromEnv(t *testing.T) {
	t.Setenv("TGASK_TOKEN", "tok")
	t.Setenv("TGASK_BOT_TOKEN", "bot-token")
	t.Setenv("TGASK_CHAT_ID", "12345")
	t.Setenv("TGASK_PORT", "8080")
	t.Setenv("TGASK_JOB_TIMEOUT", "120")

	cmd := newServeCmd()
	cfg, err := resolveServeConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.jobTimeout != 120*time.Second {
		t.Errorf("expected jobTimeout=120s, got %v", cfg.jobTimeout)
	}
}

func TestServeConfigJobTimeoutDefaultsTo3600(t *testing.T) {
	t.Setenv("TGASK_TOKEN", "tok")
	t.Setenv("TGASK_BOT_TOKEN", "bot-token")
	t.Setenv("TGASK_CHAT_ID", "12345")
	t.Setenv("TGASK_PORT", "8080")
	t.Setenv("TGASK_JOB_TIMEOUT", "")

	cmd := newServeCmd()
	cfg, err := resolveServeConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.jobTimeout != 3600*time.Second {
		t.Errorf("expected jobTimeout=3600s, got %v", cfg.jobTimeout)
	}
}

func TestServeConfigMissingTokenReturnsError(t *testing.T) {
	t.Setenv("TGASK_TOKEN", "")
	t.Setenv("TGASK_BOT_TOKEN", "bot-token")
	t.Setenv("TGASK_CHAT_ID", "12345")
	t.Setenv("TGASK_PORT", "8080")

	cmd := newServeCmd()

	_, err := resolveServeConfig(cmd)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--token") {
		t.Errorf("error %q does not contain %q", msg, "--token")
	}
	if !strings.Contains(msg, "TGASK_TOKEN") {
		t.Errorf("error %q does not contain %q", msg, "TGASK_TOKEN")
	}
}
