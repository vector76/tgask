package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/vector76/tgask/internal/telegram"
)

// newDirectCmd builds a fresh cobra.Command with the direct flags registered.
func newDirectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "direct"}
	cmd.Flags().StringP("file", "f", "", "Read prompt from file")
	cmd.Flags().StringP("output", "o", "", "Write reply to file (stdout stays clean)")
	return cmd
}

// mockDirectBotAPI is a minimal BotAPI implementation for testing doDirect.
// SendForceReplyMessage queues an update (with a matching ReplyToMessage) so that
// GetUpdates returns the reply on the next call, simulating the Telegram reply flow.
type mockDirectBotAPI struct {
	replyText    string        // text delivered as the Telegram reply
	sendErr      error         // error to return from SendForceReplyMessage
	updateQueue  chan telegram.Update
}

func newMockDirectBotAPI(replyText string) *mockDirectBotAPI {
	return &mockDirectBotAPI{
		replyText:   replyText,
		updateQueue: make(chan telegram.Update, 1),
	}
}

func newMockDirectBotAPIWithSendErr(err error) *mockDirectBotAPI {
	m := newMockDirectBotAPI("")
	m.sendErr = err
	return m
}

func (m *mockDirectBotAPI) SendForceReplyMessage(chatID int64, text string) (int, error) {
	if m.sendErr != nil {
		return 0, m.sendErr
	}
	const msgID = 42
	if m.replyText != "" {
		m.updateQueue <- telegram.Update{
			UpdateID: 1,
			Message: &telegram.Message{
				MessageID: 100,
				Text:      m.replyText,
				ReplyToMessage: &telegram.Message{MessageID: msgID},
			},
		}
	}
	return msgID, nil
}

func (m *mockDirectBotAPI) GetUpdates(offset int, timeout int, allowedUpdates []string) ([]telegram.Update, error) {
	select {
	case u := <-m.updateQueue:
		return []telegram.Update{u}, nil
	default:
		return nil, nil
	}
}

func (m *mockDirectBotAPI) DeleteMessage(chatID int64, messageID int) error { return nil }
func (m *mockDirectBotAPI) SendMessage(chatID int64, text string, replyMarkup interface{}) (int, error) {
	return 0, nil
}

// setDirectEnv sets the minimal env vars needed by doDirect.
func setDirectEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TGASK_BOT_TOKEN", "test-token")
	t.Setenv("TGASK_CHAT_ID", "12345")
}

// TestDirectMissingBotToken: no TGASK_BOT_TOKEN → exit code 1.
func TestDirectMissingBotToken(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "")
	t.Setenv("TGASK_CHAT_ID", "12345")

	code, err := doDirect(newDirectCmd(), nil, strings.NewReader(""), &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// TestDirectMissingChatID: no TGASK_CHAT_ID → exit code 1.
func TestDirectMissingChatID(t *testing.T) {
	t.Setenv("TGASK_BOT_TOKEN", "test-token")
	t.Setenv("TGASK_CHAT_ID", "")

	code, err := doDirect(newDirectCmd(), nil, strings.NewReader(""), &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// TestDirectPromptFromArg: positional arg used as prompt.
func TestDirectPromptFromArg(t *testing.T) {
	setDirectEnv(t)
	api := newMockDirectBotAPI("pong")

	var out bytes.Buffer
	code, err := doDirect(newDirectCmd(), []string{"ping"}, strings.NewReader(""), &out, api)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "pong") {
		t.Errorf("expected stdout to contain 'pong', got %q", out.String())
	}
}

// TestDirectPromptFromFile: --file flag used as prompt source.
func TestDirectPromptFromFile(t *testing.T) {
	setDirectEnv(t)

	tmp, err := os.CreateTemp(t.TempDir(), "prompt*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("from file\n")
	tmp.Close()

	cmd := newDirectCmd()
	cmd.Flags().Set("file", tmp.Name())

	api := newMockDirectBotAPI("ok")
	var out bytes.Buffer
	code, err := doDirect(cmd, nil, strings.NewReader(""), &out, api)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

// TestDirectPromptFromStdin: stdin used as prompt source.
func TestDirectPromptFromStdin(t *testing.T) {
	setDirectEnv(t)
	api := newMockDirectBotAPI("ok")

	var out bytes.Buffer
	code, err := doDirect(newDirectCmd(), nil, strings.NewReader("stdin input\n"), &out, api)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

// TestDirectReplyToStdout: reply printed to stdout when no -o flag.
func TestDirectReplyToStdout(t *testing.T) {
	setDirectEnv(t)
	api := newMockDirectBotAPI("the reply")

	var out bytes.Buffer
	code, err := doDirect(newDirectCmd(), []string{"q"}, strings.NewReader(""), &out, api)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "the reply") {
		t.Errorf("expected stdout to contain 'the reply', got %q", out.String())
	}
}

// TestDirectReplyToFile: reply written to -o file; stdout stays empty.
func TestDirectReplyToFile(t *testing.T) {
	setDirectEnv(t)
	api := newMockDirectBotAPI("answer")

	outFile := t.TempDir() + "/out.txt"
	cmd := newDirectCmd()
	cmd.Flags().Set("output", outFile)

	var stdout bytes.Buffer
	code, err := doDirect(cmd, []string{"q"}, strings.NewReader(""), &stdout, api)
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
	if string(data) != "answer" {
		t.Errorf("expected file content 'answer', got %q", string(data))
	}
}

// TestDirectSendQueryError: SendForceReplyMessage fails → exit code 1.
func TestDirectSendQueryError(t *testing.T) {
	setDirectEnv(t)
	api := newMockDirectBotAPIWithSendErr(fmt.Errorf("network error"))

	code, err := doDirect(newDirectCmd(), []string{"q"}, strings.NewReader(""), &bytes.Buffer{}, api)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// TestDirectTimeout: no reply before timeout → exit code 2.
func TestDirectTimeout(t *testing.T) {
	setDirectEnv(t)
	t.Setenv("TGASK_DEFAULT_TIMEOUT", "1")
	api := newMockDirectBotAPI("") // no reply queued

	code, err := doDirect(newDirectCmd(), []string{"q"}, strings.NewReader(""), &bytes.Buffer{}, api)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}
