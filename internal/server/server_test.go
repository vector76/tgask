package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vector76/tgask/internal/model"
)

// mockQueue implements Queuer for testing.
type mockQueue struct {
	job *model.Job
}

func (m *mockQueue) Submit(prompt string, timeout time.Duration) string {
	m.job = model.NewJob("test-id", prompt, timeout)
	return m.job.ID
}

func (m *mockQueue) GetJob(id string) (*model.Job, bool) {
	if m.job != nil && m.job.ID == id {
		return m.job, true
	}
	return nil, false
}

func newTestServer() *Server {
	return New(Config{Token: "secret", Version: "test-ver"}, nil, nil)
}

func newTestServerWithQueue(q Queuer) *Server {
	return New(Config{Token: "test-token", Version: "test-ver"}, q, nil)
}

// mockNotifier implements Notifier for testing.
type mockNotifier struct {
	called  bool
	lastMsg string
	err     error
}

func (m *mockNotifier) SendNotification(text string) error {
	m.called = true
	m.lastMsg = text
	return m.err
}

func newTestServerWithNotifier(n Notifier) *Server {
	return New(Config{Token: "test-token", Version: "test-ver"}, nil, n)
}

func authedRequest(method, path, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	return req
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestVersionEndpoint(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["version"] != "test-ver" {
		t.Errorf("expected version=test-ver, got %q", body["version"])
	}
}

func TestAskUnauthorized(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"no auth", ""},
		{"wrong token", "Bearer wrongtoken"},
	}
	s := newTestServer()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ask", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rr.Code)
			}
			var body map[string]string
			if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode body: %v", err)
			}
			if body["error"] != "unauthorized" {
				t.Errorf("expected error=unauthorized, got %q", body["error"])
			}
		})
	}
}

// TestAskValidPrompt: POST /api/v1/ask with valid JSON returns 201 with non-empty id.
func TestAskValidPrompt(t *testing.T) {
	q := &mockQueue{}
	s := newTestServerWithQueue(q)
	req := authedRequest(http.MethodPost, "/api/v1/ask", `{"prompt":"hello"}`)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["id"] == "" {
		t.Error("expected non-empty id field")
	}
}

// TestAskEmptyPrompt: POST /api/v1/ask with empty prompt returns 400.
func TestAskEmptyPrompt(t *testing.T) {
	q := &mockQueue{}
	s := newTestServerWithQueue(q)
	req := authedRequest(http.MethodPost, "/api/v1/ask", `{"prompt":""}`)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["error"] != "prompt required" {
		t.Errorf("expected error=prompt required, got %q", body["error"])
	}
}

// TestResultUnknownID: GET /api/v1/result/{id} for unknown ID returns 410.
func TestResultUnknownID(t *testing.T) {
	q := &mockQueue{}
	s := newTestServerWithQueue(q)
	req := authedRequest(http.MethodGet, "/api/v1/result/nonexistent", "")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "expired" {
		t.Errorf("expected status=expired, got %q", body["status"])
	}
}

// TestResultDoneJob: GET /api/v1/result/{id} for a done job returns 200 with reply.
func TestResultDoneJob(t *testing.T) {
	q := &mockQueue{}
	q.job = &model.Job{
		ID:     "done-id",
		Status: model.StatusDone,
		Reply:  "answer",
		DoneCh: make(chan struct{}),
	}
	close(q.job.DoneCh)
	s := newTestServerWithQueue(q)
	req := authedRequest(http.MethodGet, "/api/v1/result/done-id", "")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "done" {
		t.Errorf("expected status=done, got %q", body["status"])
	}
	if body["reply"] != "answer" {
		t.Errorf("expected reply=answer, got %q", body["reply"])
	}
}

// TestResultExpiredJob: GET /api/v1/result/{id} for an expired job returns 410.
func TestResultExpiredJob(t *testing.T) {
	q := &mockQueue{}
	q.job = &model.Job{
		ID:     "exp-id",
		Status: model.StatusExpired,
		DoneCh: make(chan struct{}),
	}
	s := newTestServerWithQueue(q)
	req := authedRequest(http.MethodGet, "/api/v1/result/exp-id", "")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "expired" {
		t.Errorf("expected status=expired, got %q", body["status"])
	}
}

// TestResultLongPollTimeout: GET /api/v1/result/{id}?wait=1 for a queued job returns 202 after ~1s.
func TestResultLongPollTimeout(t *testing.T) {
	q := &mockQueue{}
	q.job = &model.Job{
		ID:     "queued-id",
		Status: model.StatusQueued,
		DoneCh: make(chan struct{}),
	}
	s := newTestServerWithQueue(q)
	req := authedRequest(http.MethodGet, "/api/v1/result/queued-id?wait=1", "")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "queued" {
		t.Errorf("expected status=queued, got %q", body["status"])
	}
}

// TestSendValid: POST /api/v1/send with valid message returns 200 and calls notifier.
func TestSendValid(t *testing.T) {
	n := &mockNotifier{}
	s := newTestServerWithNotifier(n)
	req := authedRequest(http.MethodPost, "/api/v1/send", `{"message":"hello"}`)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]bool
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if !body["ok"] {
		t.Error("expected ok=true")
	}
	if !n.called {
		t.Error("expected notifier to be called")
	}
	if n.lastMsg != "hello" {
		t.Errorf("expected lastMsg=hello, got %q", n.lastMsg)
	}
}

// TestSendEmptyMessage: POST /api/v1/send with empty message returns 400.
func TestSendEmptyMessage(t *testing.T) {
	n := &mockNotifier{}
	s := newTestServerWithNotifier(n)
	req := authedRequest(http.MethodPost, "/api/v1/send", `{"message":""}`)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["error"] != "message required" {
		t.Errorf("expected error=message required, got %q", body["error"])
	}
}

// TestSendNotifierError: POST /api/v1/send when notifier errors returns 500.
func TestSendNotifierError(t *testing.T) {
	n := &mockNotifier{err: errors.New("telegram down")}
	s := newTestServerWithNotifier(n)
	req := authedRequest(http.MethodPost, "/api/v1/send", `{"message":"hello"}`)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected non-empty error field")
	}
}
