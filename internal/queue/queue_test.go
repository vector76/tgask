package queue

import (
	"strings"
	"testing"
	"time"
)

func TestSubmitReturnsNonEmptyID(t *testing.T) {
	q := New(nil, nil)
	id := q.Submit("hello", time.Minute)
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
}

func TestGetJobFindsSubmittedJob(t *testing.T) {
	q := New(nil, nil)
	id := q.Submit("hello", time.Minute)
	job, ok := q.GetJob(id)
	if !ok {
		t.Fatal("expected job to be found")
	}
	if job.ID != id {
		t.Fatalf("expected job ID %q, got %q", id, job.ID)
	}
	if job.Prompt != "hello" {
		t.Fatalf("expected prompt %q, got %q", "hello", job.Prompt)
	}
}

func TestGetJobReturnsFalseForUnknownID(t *testing.T) {
	q := New(nil, nil)
	_, ok := q.GetJob("nonexistent")
	if ok {
		t.Fatal("expected false for unknown ID")
	}
}

func TestSubmitReturnsDifferentIDs(t *testing.T) {
	q := New(nil, nil)
	id1 := q.Submit("first", time.Minute)
	id2 := q.Submit("second", time.Minute)
	if id1 == id2 {
		t.Fatalf("expected different IDs, got %q twice", id1)
	}
}

func TestIDsAreURLSafe(t *testing.T) {
	q := New(nil, nil)
	for i := 0; i < 20; i++ {
		id := q.Submit("prompt", time.Minute)
		if strings.ContainsAny(id, "+/=") {
			t.Fatalf("ID %q contains non-URL-safe characters", id)
		}
	}
}
