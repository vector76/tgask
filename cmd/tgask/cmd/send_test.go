package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "send"}
	cmd.Flags().StringP("file", "f", "", "Read message from file")
	return cmd
}

func mockSendServer(t *testing.T, statusCode int) (srv *httptest.Server, getReceivedBody func() map[string]string) {
	t.Helper()
	var mu sync.Mutex
	var received map[string]string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/send" {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			received = body
			mu.Unlock()
			w.WriteHeader(statusCode)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	getReceivedBody = func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		return received
	}
	return srv, getReceivedBody
}

// TestSendMessageFromArg: positional args joined as message.
func TestSendMessageFromArg(t *testing.T) {
	srv, getBody := mockSendServer(t, http.StatusOK)
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	code, err := doSend(newSendCmd(), []string{"hello", "world"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	body := getBody()
	if body["message"] != "hello world" {
		t.Errorf("expected message='hello world', got %v", body["message"])
	}
}

// TestSendMessageFromFile: --file flag used as message source.
func TestSendMessageFromFile(t *testing.T) {
	srv, getBody := mockSendServer(t, http.StatusOK)
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	tmp, err := os.CreateTemp(t.TempDir(), "msg*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("file content\n")
	tmp.Close()

	cmd := newSendCmd()
	cmd.Flags().Set("file", tmp.Name())

	code, err := doSend(cmd, nil, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	body := getBody()
	if body["message"] != "file content" {
		t.Errorf("expected message='file content', got %v", body["message"])
	}
}

// TestSendExitCode1OnServerError: 500 from server → exit code 1.
func TestSendExitCode1OnServerError(t *testing.T) {
	srv, _ := mockSendServer(t, http.StatusInternalServerError)
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	code, err := doSend(newSendCmd(), []string{"msg"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// TestSendExitCode1IfURLMissing: no TGASK_URL → exit code 1.
func TestSendExitCode1IfURLMissing(t *testing.T) {
	t.Setenv("TGASK_URL", "")
	t.Setenv("TGASK_TOKEN", "tok")

	code, err := doSend(newSendCmd(), []string{"msg"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}
