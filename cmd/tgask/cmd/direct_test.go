package cmd

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/vector76/tgask/internal/telegram"
)

// mockBotAPI tracks every call made by the Telegram component so tests can
// assert on exact interactions.
type mockBotAPI struct {
	mu             sync.Mutex
	sendForceCalls []int
	sendForceTexts []string
	deleteCalls    []int
	sendMsgCalls   []string
	nextMsgID      int
	updatesCh      chan []telegram.Update
}

func (m *mockBotAPI) SendForceReplyMessage(chatID int64, text string, parseMode string) (int, error) {
	m.mu.Lock()
	m.nextMsgID++
	id := m.nextMsgID
	m.sendForceCalls = append(m.sendForceCalls, id)
	m.sendForceTexts = append(m.sendForceTexts, text)
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
	select {
	case updates := <-m.updatesCh:
		return updates, nil
	case <-time.After(50 * time.Millisecond):
		return nil, nil
	}
}

// newDirectCmd builds a fresh cobra.Command with the direct flags registered.
func newDirectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "direct"}
	cmd.Flags().StringP("file", "f", "", "Read prompt from file")
	cmd.Flags().StringP("output", "o", "", "Write reply to file (stdout stays clean)")
	cmd.Flags().Int("timeout", 0, "Client timeout in seconds (overrides TGASK_DEFAULT_TIMEOUT)")
	return cmd
}

// feedReply sends a simulated Telegram reply update into the mock's channel.
func feedReply(mock *mockBotAPI, updateID, replyMsgID, questionMsgID int, text string) {
	mock.updatesCh <- []telegram.Update{{
		UpdateID: updateID,
		Message: &telegram.Message{
			MessageID:      replyMsgID,
			Text:           text,
			ReplyToMessage: &telegram.Message{MessageID: questionMsgID},
		},
	}}
}

// newPreloadedMock creates a mockBotAPI with a buffered updatesCh pre-loaded
// with empty updates so pollLoop can drain them and exit cleanly when Stop()
// is called, avoiding deadlocks in tg.Wait().
func newPreloadedMock() *mockBotAPI {
	mock := &mockBotAPI{updatesCh: make(chan []telegram.Update, 50)}
	for i := 0; i < 40; i++ {
		mock.updatesCh <- []telegram.Update{}
	}
	return mock
}

// waitForSendForce polls the mock until sendForceCalls has at least one entry,
// returning the first message ID. Fails the test after a deadline.
func waitForSendForce(t *testing.T, mock *mockBotAPI) int {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		mock.mu.Lock()
		n := len(mock.sendForceCalls)
		var id int
		if n > 0 {
			id = mock.sendForceCalls[0]
		}
		mock.mu.Unlock()
		if n > 0 {
			return id
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for SendForceReplyMessage call")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestDirectHappyPath(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")

	mock := newPreloadedMock()

	go func() {
		qID := waitForSendForce(t, mock)
		feedReply(mock, 1, 999, qID, "the answer")
	}()

	var out bytes.Buffer
	code, err := doDirect(newDirectCmd(), []string{"hello"}, strings.NewReader(""), &out, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("expected stdout to contain 'the answer', got %q", out.String())
	}
}

func TestDirectOutputToFile(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")

	mock := newPreloadedMock()

	go func() {
		qID := waitForSendForce(t, mock)
		feedReply(mock, 1, 999, qID, "file answer")
	}()

	outFile := t.TempDir() + "/out.txt"
	cmd := newDirectCmd()
	cmd.Flags().Set("output", outFile)

	var stdout bytes.Buffer
	code, err := doDirect(cmd, []string{"hello"}, strings.NewReader(""), &stdout, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "file answer") {
		t.Errorf("expected file to contain 'file answer', got %q", string(data))
	}
}

func TestDirectPromptFromArg(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")

	mock := newPreloadedMock()

	go func() {
		qID := waitForSendForce(t, mock)
		feedReply(mock, 1, 999, qID, "ok")
	}()

	var out bytes.Buffer
	code, err := doDirect(newDirectCmd(), []string{"my prompt"}, strings.NewReader(""), &out, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	mock.mu.Lock()
	text := mock.sendForceTexts[0]
	mock.mu.Unlock()
	if !strings.Contains(text, "my prompt") {
		t.Errorf("expected sent text to contain 'my prompt', got %q", text)
	}
}

func TestDirectPromptFromFile(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")

	tmp, err := os.CreateTemp(t.TempDir(), "prompt*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("from file\n")
	tmp.Close()

	cmd := newDirectCmd()
	cmd.Flags().Set("file", tmp.Name())

	mock := newPreloadedMock()

	go func() {
		qID := waitForSendForce(t, mock)
		feedReply(mock, 1, 999, qID, "ok")
	}()

	var out bytes.Buffer
	code, err := doDirect(cmd, nil, strings.NewReader(""), &out, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	mock.mu.Lock()
	text := mock.sendForceTexts[0]
	mock.mu.Unlock()
	if !strings.Contains(text, "from file") {
		t.Errorf("expected sent text to contain 'from file', got %q", text)
	}
}

func TestDirectPromptFromStdin(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")

	mock := newPreloadedMock()

	go func() {
		qID := waitForSendForce(t, mock)
		feedReply(mock, 1, 999, qID, "ok")
	}()

	var out bytes.Buffer
	code, err := doDirect(newDirectCmd(), nil, strings.NewReader("stdin prompt\n"), &out, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	mock.mu.Lock()
	text := mock.sendForceTexts[0]
	mock.mu.Unlock()
	if !strings.Contains(text, "stdin prompt") {
		t.Errorf("expected sent text to contain 'stdin prompt', got %q", text)
	}
}

func TestDirectExpiryExitCode2(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")
	t.Setenv("TGASK_DEFAULT_TIMEOUT", "1")

	mock := newPreloadedMock()
	// No reply fed — timeout will fire after ~1s.

	code, err := doDirect(newDirectCmd(), []string{"q"}, strings.NewReader(""), &bytes.Buffer{}, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}

	mock.mu.Lock()
	deleteCount := len(mock.deleteCalls)
	var expiryMsg string
	if len(mock.sendMsgCalls) > 0 {
		expiryMsg = mock.sendMsgCalls[0]
	}
	mock.mu.Unlock()

	if deleteCount != 1 {
		t.Errorf("expected 1 DeleteMessage call, got %d", deleteCount)
	}
	if !strings.Contains(expiryMsg, "expired") {
		t.Errorf("expected expiry notice containing 'expired', got %q", expiryMsg)
	}
}

func TestDirectMissingBotToken(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "")
	t.Setenv("TGASK_CHAT_ID", "123")

	mock := &mockBotAPI{}
	code, err := doDirect(newDirectCmd(), nil, strings.NewReader(""), &bytes.Buffer{}, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestDirectMissingChatID(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "")

	mock := &mockBotAPI{}
	code, err := doDirect(newDirectCmd(), nil, strings.NewReader(""), &bytes.Buffer{}, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// TestDirectTimeoutFlagOverridesEnv: --timeout flag takes precedence over TGASK_DEFAULT_TIMEOUT.
func TestDirectTimeoutFlagOverridesEnv(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "bot")
	t.Setenv("TGASK_CHAT_ID", "123")
	t.Setenv("TGASK_DEFAULT_TIMEOUT", "9999")

	mock := newPreloadedMock()
	// No reply fed — timeout will fire after the flag value (1s).

	cmd := newDirectCmd()
	cmd.Flags().Set("timeout", "1")

	code, err := doDirect(cmd, []string{"q"}, strings.NewReader(""), &bytes.Buffer{}, mock)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected exit 2 (expired), got %d", code)
	}
}
