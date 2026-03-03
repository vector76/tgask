package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// newAskCmd builds a fresh cobra.Command with the ask flags registered.
func newAskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ask"}
	cmd.Flags().StringP("file", "f", "", "Read prompt from file")
	cmd.Flags().StringP("output", "o", "", "Write reply to file (stdout stays clean)")
	return cmd
}

// mockAskServer creates a test server that:
//   - Handles POST /api/v1/ask — stores request body, responds with 201 + id
//   - Handles GET /api/v1/result/{id} — uses the provided handler
func mockAskServer(t *testing.T, resultHandler http.HandlerFunc) (srv *httptest.Server, getReceivedBody func() map[string]interface{}) {
	t.Helper()
	var received atomic.Value
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ask":
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			received.Store(body)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"id": "test-id"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/result/"):
			resultHandler(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	getReceivedBody = func() map[string]interface{} {
		v := received.Load()
		if v == nil {
			return nil
		}
		return v.(map[string]interface{})
	}
	return srv, getReceivedBody
}

// doneHandler responds with 200 done + given reply.
func doneHandler(reply string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "done", "reply": reply})
	}
}

// TestAskPromptFromArg: positional arg used as prompt.
func TestAskPromptFromArg(t *testing.T) {
	srv, getBody := mockAskServer(t, doneHandler("pong"))
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	var out bytes.Buffer
	code, err := doAsk(newAskCmd(), []string{"hello"}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	body := getBody()
	if body["prompt"] != "hello" {
		t.Errorf("expected prompt=hello, got %v", body["prompt"])
	}
}

// TestAskPromptFromFile: --file flag used as prompt source.
func TestAskPromptFromFile(t *testing.T) {
	srv, getBody := mockAskServer(t, doneHandler("ok"))
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	tmp, err := os.CreateTemp(t.TempDir(), "prompt*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("from file\n")
	tmp.Close()

	cmd := newAskCmd()
	cmd.Flags().Set("file", tmp.Name())

	var out bytes.Buffer
	code, err := doAsk(cmd, nil, strings.NewReader(""), &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	body := getBody()
	if body["prompt"] != "from file" {
		t.Errorf("expected prompt='from file', got %v", body["prompt"])
	}
}

// TestAskPromptFromStdin: stdin used as prompt source.
func TestAskPromptFromStdin(t *testing.T) {
	srv, getBody := mockAskServer(t, doneHandler("ok"))
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	var out bytes.Buffer
	code, err := doAsk(newAskCmd(), nil, strings.NewReader("stdin input\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	body := getBody()
	if body["prompt"] != "stdin input" {
		t.Errorf("expected prompt='stdin input', got %v", body["prompt"])
	}
}

// TestAskOutputToFile: reply written to -o file; stdout stays empty.
func TestAskOutputToFile(t *testing.T) {
	srv, _ := mockAskServer(t, doneHandler("answer"))
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	outFile := t.TempDir() + "/out.txt"
	cmd := newAskCmd()
	cmd.Flags().Set("output", outFile)

	var stdout bytes.Buffer
	code, err := doAsk(cmd, []string{"q"}, strings.NewReader(""), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	// stdout must be empty
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}

	// output file must contain the reply
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "answer" {
		t.Errorf("expected file content 'answer', got %q", string(data))
	}
}

// TestAskOutputToStdout: reply printed to stdout when no -o flag.
func TestAskOutputToStdout(t *testing.T) {
	srv, _ := mockAskServer(t, doneHandler("the reply"))
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	var out bytes.Buffer
	code, err := doAsk(newAskCmd(), []string{"q"}, strings.NewReader(""), &out)
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

// TestAskExitCode2On410: 410 from poll → exit code 2.
func TestAskExitCode2On410(t *testing.T) {
	srv, _ := mockAskServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]string{"status": "expired"})
	})
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	var out bytes.Buffer
	code, err := doAsk(newAskCmd(), []string{"q"}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

// TestAskExitCode1OnServerError: 500 from poll → exit code 1.
func TestAskExitCode1OnServerError(t *testing.T) {
	srv, _ := mockAskServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	var out bytes.Buffer
	code, err := doAsk(newAskCmd(), []string{"q"}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// TestAskRetryLoop: 202 twice then 200 — client polls three times total, exits 0.
func TestAskRetryLoop(t *testing.T) {
	var pollCount int32
	srv, _ := mockAskServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pollCount, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "done", "reply": "final"})
		}
	})
	defer srv.Close()
	t.Setenv("TGASK_URL", srv.URL)
	t.Setenv("TGASK_TOKEN", "tok")

	var out bytes.Buffer
	code, err := doAsk(newAskCmd(), []string{"q"}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if pollCount != 3 {
		t.Errorf("expected 3 polls, got %d", pollCount)
	}
	if !strings.Contains(out.String(), "final") {
		t.Errorf("expected stdout to contain 'final', got %q", out.String())
	}
}
