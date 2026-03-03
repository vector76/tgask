package queue

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vector76/tgask/internal/model"
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

func TestWorkerSerialProcessing(t *testing.T) {
	// secondDispatched is closed when dispatch is called for the second job.
	secondDispatched := make(chan struct{})
	firstJob := make(chan *model.Job, 1)

	var mu sync.Mutex
	dispatchCount := 0

	dispatch := func(job *model.Job) {
		mu.Lock()
		dispatchCount++
		n := dispatchCount
		mu.Unlock()

		if n == 1 {
			firstJob <- job
		} else {
			close(secondDispatched)
		}
	}

	q := New(dispatch, func(*model.Job) {})
	q.Start()

	id1 := q.Submit("first", time.Minute)
	id2 := q.Submit("second", time.Minute)

	// Wait for the first job to be dispatched.
	j1 := <-firstJob

	// Confirm second dispatch hasn't happened yet.
	select {
	case <-secondDispatched:
		t.Fatal("second job dispatched before first job completed")
	default:
	}

	// Complete the first job by sending a reply.
	j1.ReplyCh <- "reply"
	<-j1.DoneCh

	// Now the second job should be dispatched.
	select {
	case <-secondDispatched:
		// OK
	case <-time.After(time.Second):
		t.Fatal("second job was not dispatched after first job completed")
	}

	// Retrieve the second job and complete it so the worker doesn't leak.
	j2, _ := q.GetJob(id2)
	j2.ReplyCh <- "reply2"
	<-j2.DoneCh

	_ = id1
}

func TestWorkerExpiry(t *testing.T) {
	expiryCalled := make(chan *model.Job, 1)

	dispatch := func(job *model.Job) {}
	expiry := func(job *model.Job) {
		expiryCalled <- job
	}

	q := New(dispatch, expiry)
	q.Start()

	id := q.Submit("expire me", 50*time.Millisecond)

	select {
	case job := <-expiryCalled:
		if job.Status != model.StatusExpired {
			t.Fatalf("expected StatusExpired, got %q", job.Status)
		}
		select {
		case <-job.DoneCh:
			// DoneCh is closed — OK
		default:
			t.Fatal("expected DoneCh to be closed after expiry")
		}
	case <-time.After(time.Second):
		t.Fatal("expiry callback was not called within timeout")
	}

	_ = id
}

func TestWorkerReplyRouting(t *testing.T) {
	dispatched := make(chan *model.Job, 1)

	dispatch := func(job *model.Job) {
		dispatched <- job
	}

	q := New(dispatch, func(*model.Job) {})
	q.Start()

	id := q.Submit("hello", time.Minute)

	job := <-dispatched
	job.ReplyCh <- "the answer"

	select {
	case <-job.DoneCh:
		// closed — OK
	case <-time.After(time.Second):
		t.Fatal("DoneCh was not closed after reply")
	}

	retrieved, ok := q.GetJob(id)
	if !ok {
		t.Fatal("job not found")
	}
	if retrieved.Status != model.StatusDone {
		t.Fatalf("expected StatusDone, got %q", retrieved.Status)
	}
	if retrieved.Reply != "the answer" {
		t.Fatalf("expected reply %q, got %q", "the answer", retrieved.Reply)
	}
}
