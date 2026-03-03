package model

import (
	"testing"
	"time"
)

func TestNewJob(t *testing.T) {
	timeout := 30 * time.Second
	before := time.Now()
	job := NewJob("test-id", "test prompt", timeout)
	after := time.Now()

	if job.ID != "test-id" {
		t.Errorf("ID = %q, want %q", job.ID, "test-id")
	}
	if job.Prompt != "test prompt" {
		t.Errorf("Prompt = %q, want %q", job.Prompt, "test prompt")
	}
	if job.Status != StatusQueued {
		t.Errorf("Status = %q, want %q", job.Status, StatusQueued)
	}
	if job.Timeout != timeout {
		t.Errorf("Timeout = %v, want %v", job.Timeout, timeout)
	}
	if job.ReplyCh == nil {
		t.Error("ReplyCh is nil")
	}
	if cap(job.ReplyCh) != 1 {
		t.Errorf("ReplyCh capacity = %d, want 1", cap(job.ReplyCh))
	}
	if job.DoneCh == nil {
		t.Error("DoneCh is nil")
	}
	if job.ExpiresAt.Before(before.Add(timeout)) || job.ExpiresAt.After(after.Add(timeout)) {
		t.Errorf("ExpiresAt = %v, want between %v and %v", job.ExpiresAt, before.Add(timeout), after.Add(timeout))
	}
}

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusQueued, "queued"},
		{StatusAwaitingReply, "awaiting_reply"},
		{StatusDone, "done"},
		{StatusExpired, "expired"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("Status %q = %q, want %q", tt.status, string(tt.status), tt.want)
		}
	}
}
