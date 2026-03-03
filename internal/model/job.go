package model

import "time"

type Status string

const (
	StatusQueued        Status = "queued"
	StatusAwaitingReply Status = "awaiting_reply"
	StatusDone          Status = "done"
	StatusExpired       Status = "expired"
)

type Job struct {
	ID                string
	Prompt            string
	Status            Status
	Reply             string
	TelegramMessageID int
	ReplyCh           chan string
	DoneCh            chan struct{}
	Timeout           time.Duration
	ExpiresAt         time.Time
}

func NewJob(id, prompt string, timeout time.Duration) *Job {
	return &Job{
		ID:        id,
		Prompt:    prompt,
		Status:    StatusQueued,
		ReplyCh:   make(chan string, 1),
		DoneCh:    make(chan struct{}),
		Timeout:   timeout,
		ExpiresAt: time.Now().Add(timeout),
	}
}
