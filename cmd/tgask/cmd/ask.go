package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var askCmd = &cobra.Command{
	Use:   "ask [prompt]",
	Short: "Ask a question via Telegram and wait for a reply",
	RunE:  runAsk,
}

func init() {
	askCmd.Flags().StringP("file", "f", "", "Read prompt from file")
	askCmd.Flags().StringP("output", "o", "", "Write reply to file (stdout stays clean)")
}

func runAsk(cmd *cobra.Command, args []string) error {
	code, err := doAsk(cmd, args, os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// doAsk contains the testable core logic of the ask command.
// It returns an exit code (0 = success, 1 = error, 2 = expired) and any unexpected error.
func doAsk(cmd *cobra.Command, args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	_ = godotenv.Load()

	tgaskURL := os.Getenv("TGASK_URL")
	token := os.Getenv("TGASK_TOKEN")
	if tgaskURL == "" {
		fmt.Fprintln(os.Stderr, "error: TGASK_URL is not set")
		return 1, nil
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "error: TGASK_TOKEN is not set")
		return 1, nil
	}

	timeoutSecs := 300
	if s := os.Getenv("TGASK_DEFAULT_TIMEOUT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			timeoutSecs = n
		}
	}

	// Resolve prompt: arg > --file > stdin
	var prompt string
	if len(args) > 0 {
		prompt = args[0]
	} else {
		fileFlag, _ := cmd.Flags().GetString("file")
		if fileFlag != "" {
			data, err := os.ReadFile(fileFlag)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1, nil
			}
			prompt = strings.TrimRight(string(data), "\n")
		} else {
			data, err := io.ReadAll(stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
				return 1, nil
			}
			prompt = strings.TrimRight(string(data), "\n")
		}
	}

	// POST /api/v1/ask
	body, _ := json.Marshal(map[string]interface{}{"prompt": prompt, "timeout": timeoutSecs})
	req, _ := http.NewRequest("POST", tgaskURL+"/api/v1/ask", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1, nil
	}
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "error: server returned %d\n", resp.StatusCode)
		return 1, nil
	}
	var askResp struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&askResp)
	resp.Body.Close()
	id := askResp.ID

	outputFile, _ := cmd.Flags().GetString("output")
	pollURL := fmt.Sprintf("%s/api/v1/result/%s?wait=30", tgaskURL, id)

	// Long-poll loop
	for {
		req, _ := http.NewRequest("GET", pollURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		pollResp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1, nil
		}

		var result struct {
			Status string `json:"status"`
			Reply  string `json:"reply"`
		}
		json.NewDecoder(pollResp.Body).Decode(&result)
		pollResp.Body.Close()

		switch pollResp.StatusCode {
		case http.StatusOK:
			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(result.Reply), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error writing output file: %v\n", err)
					return 1, nil
				}
			} else {
				fmt.Fprintln(stdout, result.Reply)
			}
			return 0, nil
		case http.StatusGone:
			fmt.Fprintln(os.Stderr, "error: query expired with no reply")
			return 2, nil
		case http.StatusAccepted:
			continue
		default:
			fmt.Fprintf(os.Stderr, "error: server returned %d\n", pollResp.StatusCode)
			return 1, nil
		}
	}
}
