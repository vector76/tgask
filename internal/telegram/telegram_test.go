package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/vector76/tgask/internal/model"
)

// mockBotAPI implements BotAPI for testing.
type mockBotAPI struct {
	updatesCh       chan []Update
	forceReplyMsgID int // ID returned by SendForceReplyMessage
	sendMsgCalls    []sendMsgCall
	deleteMsgCalls  []int
}

type sendMsgCall struct {
	chatID int64
	text   string
}

func (m *mockBotAPI) GetUpdates(offset, timeout int, allowed []string) ([]Update, error) {
	updates := <-m.updatesCh
	return updates, nil
}

func (m *mockBotAPI) SendMessage(chatID int64, text string, replyMarkup interface{}) (int, error) {
	m.sendMsgCalls = append(m.sendMsgCalls, sendMsgCall{chatID, text})
	return 0, nil
}

func (m *mockBotAPI) SendForceReplyMessage(chatID int64, text string) (int, error) {
	return m.forceReplyMsgID, nil
}

func (m *mockBotAPI) DeleteMessage(chatID int64, messageID int) error {
	m.deleteMsgCalls = append(m.deleteMsgCalls, messageID)
	return nil
}

func newTestTelegram() (*Telegram, *mockBotAPI) {
	mock := &mockBotAPI{updatesCh: make(chan []Update, 1)}
	tg := New(mock, Config{})
	return tg, mock
}

// TestOffsetAdvancement verifies that t.offset advances to updateID+1 after processing.
func TestOffsetAdvancement(t *testing.T) {
	tg, mock := newTestTelegram()

	// Feed two updates then stop
	mock.updatesCh <- []Update{
		{UpdateID: 10, Message: &Message{MessageID: 1, Text: "hi"}},
		{UpdateID: 20, Message: &Message{MessageID: 2, Text: "ho"}},
	}

	tg.Start()

	// Give the poll loop time to process the updates.
	time.Sleep(50 * time.Millisecond)
	tg.Stop()

	// Unblock the GetUpdates call so the goroutine can observe stopCh and exit.
	mock.updatesCh <- []Update{}

	// Wait for the goroutine to fully exit before reading offset.
	tg.Wait()

	if tg.offset != 21 {
		t.Errorf("expected offset 21, got %d", tg.offset)
	}
}

// TestReplyRouting verifies that a reply update is routed to the correct job's ReplyCh.
func TestReplyRouting(t *testing.T) {
	tg, mock := newTestTelegram()

	job := model.NewJob("job1", "prompt", time.Minute)
	const sentMsgID = 42
	tg.inFlight[sentMsgID] = job

	mock.updatesCh <- []Update{
		{
			UpdateID: 5,
			Message: &Message{
				MessageID: 99,
				Text:      "my answer",
				ReplyToMessage: &Message{
					MessageID: sentMsgID,
				},
			},
		},
	}

	tg.Start()

	select {
	case reply := <-job.ReplyCh:
		if reply != "my answer" {
			t.Errorf("expected 'my answer', got %q", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply on ReplyCh")
	}

	tg.Stop()
	mock.updatesCh <- []Update{}
	tg.Wait()

	// inFlight entry should be removed after routing
	tg.inFlightMu.Lock()
	_, still := tg.inFlight[sentMsgID]
	tg.inFlightMu.Unlock()
	if still {
		t.Error("expected inFlight entry to be deleted after routing")
	}
}

// TestNonReplyDiscarded verifies that a message without ReplyToMessage does not panic
// and does not send anything to any channel.
func TestNonReplyDiscarded(t *testing.T) {
	tg, mock := newTestTelegram()

	job := model.NewJob("job2", "prompt", time.Minute)
	tg.inFlight[100] = job

	mock.updatesCh <- []Update{
		{
			UpdateID: 7,
			Message: &Message{
				MessageID: 200,
				Text:      "plain message, no reply",
			},
		},
	}

	tg.Start()
	time.Sleep(50 * time.Millisecond)
	tg.Stop()
	mock.updatesCh <- []Update{}
	tg.Wait()

	// ReplyCh should have received nothing
	select {
	case got := <-job.ReplyCh:
		t.Errorf("expected no reply, got %q", got)
	default:
	}
}

// TestSendQuery verifies that SendQuery sets TelegramMessageID, Status, and registers the job in inFlight.
func TestSendQuery(t *testing.T) {
	tg, mock := newTestTelegram()
	mock.forceReplyMsgID = 42

	job := model.NewJob("job1", "test prompt", time.Minute)
	if err := tg.SendQuery(job); err != nil {
		t.Fatalf("SendQuery returned error: %v", err)
	}

	if job.TelegramMessageID != 42 {
		t.Errorf("expected TelegramMessageID=42, got %d", job.TelegramMessageID)
	}
	if job.Status != model.StatusAwaitingReply {
		t.Errorf("expected StatusAwaitingReply, got %q", job.Status)
	}
	tg.inFlightMu.Lock()
	got, ok := tg.inFlight[42]
	tg.inFlightMu.Unlock()
	if !ok {
		t.Fatal("expected inFlight[42] to be set")
	}
	if got != job {
		t.Error("expected inFlight[42] to point to job")
	}
}

// TestSendNotification verifies that SendNotification calls SendMessage with the correct args.
func TestSendNotification(t *testing.T) {
	tg, mock := newTestTelegram()
	tg.cfg.ChatID = 12345

	if err := tg.SendNotification("hello"); err != nil {
		t.Fatalf("SendNotification returned error: %v", err)
	}

	if len(mock.sendMsgCalls) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(mock.sendMsgCalls))
	}
	if mock.sendMsgCalls[0].chatID != 12345 {
		t.Errorf("expected chatID=12345, got %d", mock.sendMsgCalls[0].chatID)
	}
	if mock.sendMsgCalls[0].text != "hello" {
		t.Errorf("expected text %q, got %q", "hello", mock.sendMsgCalls[0].text)
	}
}

// TestHandleExpiry verifies that HandleExpiry removes the job from inFlight, deletes the message, and sends an expiry notice.
func TestHandleExpiry(t *testing.T) {
	tg, mock := newTestTelegram()
	tg.cfg.ChatID = 12345

	job := model.NewJob("job1", "prompt", time.Minute)
	const msgID = 55
	job.TelegramMessageID = msgID
	tg.inFlight[msgID] = job

	tg.HandleExpiry(job)

	tg.inFlightMu.Lock()
	_, still := tg.inFlight[msgID]
	tg.inFlightMu.Unlock()
	if still {
		t.Error("expected inFlight entry to be removed after HandleExpiry")
	}

	if len(mock.deleteMsgCalls) != 1 || mock.deleteMsgCalls[0] != msgID {
		t.Errorf("expected DeleteMessage(%d), got %v", msgID, mock.deleteMsgCalls)
	}

	found := false
	for _, c := range mock.sendMsgCalls {
		if strings.Contains(c.text, "expired") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SendMessage containing 'expired', calls: %v", mock.sendMsgCalls)
	}
}

// TestUnknownMessageIDDiscarded verifies that a reply to an unknown message_id does not panic.
func TestUnknownMessageIDDiscarded(t *testing.T) {
	tg, mock := newTestTelegram()

	// inFlight is empty; the reply references a non-existent message_id
	mock.updatesCh <- []Update{
		{
			UpdateID: 3,
			Message: &Message{
				MessageID: 55,
				Text:      "reply to nobody",
				ReplyToMessage: &Message{
					MessageID: 999,
				},
			},
		},
	}

	tg.Start()
	time.Sleep(50 * time.Millisecond)
	tg.Stop()
	mock.updatesCh <- []Update{}
	tg.Wait()

	// No panic = success; nothing more to assert
}
