package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vector76/tgask/internal/model"
	"github.com/vector76/tgask/internal/queue"
	"github.com/vector76/tgask/internal/server"
	"github.com/vector76/tgask/internal/telegram"
)

type mockBotAPI struct {
	mu                  sync.Mutex
	sendForceCalls      []int
	sendForceTimestamps []time.Time
	deleteCalls         []int
	sendMsgCalls        []string
	nextMsgID           int
	updatesCh           chan []telegram.Update
}

func (m *mockBotAPI) SendForceReplyMessage(chatID int64, text string, parseMode string) (int, error) {
	m.mu.Lock()
	m.nextMsgID++
	id := m.nextMsgID
	m.sendForceCalls = append(m.sendForceCalls, id)
	m.sendForceTimestamps = append(m.sendForceTimestamps, time.Now())
	m.mu.Unlock()
	return id, nil
}

func (m *mockBotAPI) SendMessage(chatID int64, text string, _ interface{}) (int, error) {
	m.mu.Lock()
	m.sendMsgCalls = append(m.sendMsgCalls, text)
	m.mu.Unlock()
	return 0, nil
}

func (m *mockBotAPI) DeleteMessage(chatID int64, messageID int) error {
	m.mu.Lock()
	m.deleteCalls = append(m.deleteCalls, messageID)
	m.mu.Unlock()
	return nil
}

func (m *mockBotAPI) GetUpdates(offset, timeout int, _ []string) ([]telegram.Update, error) {
	updates := <-m.updatesCh
	return updates, nil
}

func buildStack(t *testing.T) (serverURL string, mock *mockBotAPI, cleanup func()) {
	t.Helper()
	mock = &mockBotAPI{
		updatesCh: make(chan []telegram.Update, 10),
		nextMsgID: 100,
	}

	tg := telegram.New(mock, telegram.Config{ChatID: 12345})

	dispatch := func(job *model.Job) error { return tg.SendQuery(job) }
	q := queue.New(dispatch, tg.HandleExpiry)

	srv := server.New(server.Config{Token: "test-token", Version: "test"}, q, tg)
	httpSrv := httptest.NewServer(srv)

	q.Start()
	tg.Start()

	cleanup = func() {
		tg.Stop()
		httpSrv.Close()
		// drain updatesCh to unblock any waiting GetUpdates
		for len(mock.updatesCh) > 0 {
			<-mock.updatesCh
		}
		// send empty updates to unblock the pollLoop goroutine so it can exit
		mock.updatesCh <- []telegram.Update{}
		tg.Wait()
	}

	return httpSrv.URL, mock, cleanup
}

func authDo(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("doing request: %v", err)
	}
	return resp
}

func TestHappyPath(t *testing.T) {
	serverURL, mock, cleanup := buildStack(t)
	t.Cleanup(cleanup)

	// POST /api/v1/ask
	resp := authDo(t, "POST", serverURL+"/api/v1/ask", `{"prompt":"hello","timeout":60}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var askResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&askResp); err != nil {
		t.Fatalf("decoding ask response: %v", err)
	}
	resp.Body.Close()
	id := askResp["id"]
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	// Wait for SendForceReplyMessage to be called
	var messageID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		if len(mock.sendForceCalls) > 0 {
			messageID = mock.sendForceCalls[0]
		}
		mock.mu.Unlock()
		if messageID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if messageID == 0 {
		t.Fatal("timed out waiting for SendForceReplyMessage")
	}

	// Feed reply update
	mock.updatesCh <- []telegram.Update{{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 999,
			Text:      "my answer",
			ReplyToMessage: &telegram.Message{MessageID: messageID},
		},
	}}

	// GET /api/v1/result/{id}?wait=5
	resp = authDo(t, "GET", serverURL+"/api/v1/result/"+id+"?wait=5", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var resultResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&resultResp); err != nil {
		t.Fatalf("decoding result response: %v", err)
	}
	resp.Body.Close()
	if resultResp["status"] != "done" {
		t.Fatalf("expected status 'done', got %q", resultResp["status"])
	}
	if resultResp["reply"] != "my answer" {
		t.Fatalf("expected reply 'my answer', got %q", resultResp["reply"])
	}
}

func TestExpiry(t *testing.T) {
	serverURL, mock, cleanup := buildStack(t)
	t.Cleanup(cleanup)

	// POST /api/v1/ask with 1-second timeout
	resp := authDo(t, "POST", serverURL+"/api/v1/ask", `{"prompt":"expire me","timeout":1}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var askResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&askResp); err != nil {
		t.Fatalf("decoding ask response: %v", err)
	}
	resp.Body.Close()
	id := askResp["id"]

	// Sleep 2 seconds to allow expiry
	time.Sleep(2 * time.Second)

	// GET /api/v1/result/{id} - expect 410
	resp = authDo(t, "GET", serverURL+"/api/v1/result/"+id, "")
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expected 410, got %d", resp.StatusCode)
	}
	var resultResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&resultResp); err != nil {
		t.Fatalf("decoding result response: %v", err)
	}
	resp.Body.Close()
	if resultResp["status"] != "expired" {
		t.Fatalf("expected status 'expired', got %q", resultResp["status"])
	}

	// Confirm deleteCalls has one entry
	mock.mu.Lock()
	numDeletes := len(mock.deleteCalls)
	mock.mu.Unlock()
	if numDeletes != 1 {
		t.Fatalf("expected 1 delete call, got %d", numDeletes)
	}

	// Confirm sendMsgCalls contains a message with "expired"
	mock.mu.Lock()
	msgs := make([]string, len(mock.sendMsgCalls))
	copy(msgs, mock.sendMsgCalls)
	mock.mu.Unlock()
	var foundExpired bool
	for _, msg := range msgs {
		if strings.Contains(msg, "expired") {
			foundExpired = true
			break
		}
	}
	if !foundExpired {
		t.Fatalf("expected sendMsgCalls to contain 'expired', got %v", msgs)
	}

	// Send empty update to unblock pollLoop
	mock.updatesCh <- []telegram.Update{}
}

func TestSendWhileAskInFlight(t *testing.T) {
	serverURL, mock, cleanup := buildStack(t)
	t.Cleanup(cleanup)

	// POST /api/v1/ask with long timeout
	resp := authDo(t, "POST", serverURL+"/api/v1/ask", `{"prompt":"long running","timeout":60}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var askResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&askResp); err != nil {
		t.Fatalf("decoding ask response: %v", err)
	}
	resp.Body.Close()

	// Wait for the job to be dispatched (job is in-flight)
	var messageID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		if len(mock.sendForceCalls) > 0 {
			messageID = mock.sendForceCalls[0]
		}
		mock.mu.Unlock()
		if messageID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if messageID == 0 {
		t.Fatal("timed out waiting for SendForceReplyMessage")
	}

	// POST /api/v1/send - must return within 1 second (must not block on the queue)
	start := time.Now()
	resp = authDo(t, "POST", serverURL+"/api/v1/send", `{"message":"notification"}`)
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if elapsed > time.Second {
		t.Fatalf("send took %v, expected < 1s", elapsed)
	}

	// Resolve the in-flight job by feeding a reply update to unblock cleanup
	mock.updatesCh <- []telegram.Update{{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 999,
			Text:      "cleanup reply",
			ReplyToMessage: &telegram.Message{MessageID: messageID},
		},
	}}
}

func TestSerialQueue(t *testing.T) {
	serverURL, mock, cleanup := buildStack(t)
	t.Cleanup(cleanup)

	// Submit 3 jobs simultaneously from separate goroutines.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := authDo(t, "POST", serverURL+"/api/v1/ask", `{"prompt":"concurrent job","timeout":60}`)
			resp.Body.Close()
		}()
	}
	wg.Wait()

	// waitForNCalls blocks until sendForceCalls has at least n entries.
	waitForNCalls := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mock.mu.Lock()
			count := len(mock.sendForceCalls)
			mock.mu.Unlock()
			if count >= n {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d SendForceReplyMessage call(s)", n)
	}

	feedReply := func(updateID, replyMsgID, questionMsgID int, text string) {
		mock.updatesCh <- []telegram.Update{{
			UpdateID: updateID,
			Message: &telegram.Message{
				MessageID: replyMsgID,
				Text:      text,
				ReplyToMessage: &telegram.Message{MessageID: questionMsgID},
			},
		}}
	}

	// Wait for first job to be dispatched.
	waitForNCalls(1)

	// Record the time before feeding reply 1, then feed it.
	beforeFeed1 := time.Now()
	mock.mu.Lock()
	msgID1 := mock.sendForceCalls[0]
	mock.mu.Unlock()
	feedReply(1, 1001, msgID1, "reply1")

	// Wait for second job to be dispatched — proves first job completed before second started.
	waitForNCalls(2)
	mock.mu.Lock()
	callTime2 := mock.sendForceTimestamps[1]
	msgID2 := mock.sendForceCalls[1]
	mock.mu.Unlock()
	if callTime2.Before(beforeFeed1) {
		t.Errorf("second SendForceReplyMessage (at %v) occurred before reply 1 was fed (at %v)", callTime2, beforeFeed1)
	}

	// Record the time before feeding reply 2, then feed it.
	beforeFeed2 := time.Now()
	feedReply(2, 1002, msgID2, "reply2")

	// Wait for third job to be dispatched — proves second job completed before third started.
	waitForNCalls(3)
	mock.mu.Lock()
	callTime3 := mock.sendForceTimestamps[2]
	msgID3 := mock.sendForceCalls[2]
	mock.mu.Unlock()
	if callTime3.Before(beforeFeed2) {
		t.Errorf("third SendForceReplyMessage (at %v) occurred before reply 2 was fed (at %v)", callTime3, beforeFeed2)
	}

	// Feed reply for job 3 to allow clean shutdown.
	feedReply(3, 1003, msgID3, "reply3")
}
