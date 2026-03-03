package queue

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/vector76/tgask/internal/model"
)

type DispatchFunc func(job *model.Job)
type ExpiryFunc func(job *model.Job)

type Queue struct {
	mu       sync.Mutex
	jobs     map[string]*model.Job
	pending  chan *model.Job // buffered, size 256
	dispatch DispatchFunc
	expiry   ExpiryFunc
}

func New(dispatch DispatchFunc, expiry ExpiryFunc) *Queue {
	return &Queue{
		jobs:     make(map[string]*model.Job),
		pending:  make(chan *model.Job, 256),
		dispatch: dispatch,
		expiry:   expiry,
	}
}

func (q *Queue) Submit(prompt string, timeout time.Duration) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	id := base64.RawURLEncoding.EncodeToString(b)

	job := model.NewJob(id, prompt, timeout)

	q.mu.Lock()
	q.jobs[id] = job
	q.mu.Unlock()

	q.pending <- job

	return id
}

func (q *Queue) GetJob(id string) (*model.Job, bool) {
	q.mu.Lock()
	job, ok := q.jobs[id]
	q.mu.Unlock()
	return job, ok
}
