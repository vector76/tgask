package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:   "send [message]",
	Short: "Send a plain notification via Telegram",
	RunE:  runSend,
}

func init() {
	sendCmd.Flags().StringP("file", "f", "", "Read message from file")
}

func runSend(cmd *cobra.Command, args []string) error {
	code, err := doSend(cmd, args, os.Stdin)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// doSend contains the testable core logic of the send command.
// It returns an exit code (0 = success, 1 = error) and any unexpected error.
func doSend(cmd *cobra.Command, args []string, stdin io.Reader) (int, error) {
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

	// Message resolution: positional arg → --file → stdin
	var message string
	if len(args) > 0 {
		message = strings.Join(args, " ")
	} else if f, _ := cmd.Flags().GetString("file"); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
			return 1, nil
		}
		message = strings.TrimRight(string(data), "\n")
	} else {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			return 1, nil
		}
		message = strings.TrimRight(string(data), "\n")
	}

	body, _ := json.Marshal(map[string]string{"message": message})
	req, _ := http.NewRequest("POST", tgaskURL+"/api/v1/send", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "error: server returned %d\n", resp.StatusCode)
		return 1, nil
	}
	return 0, nil
}
