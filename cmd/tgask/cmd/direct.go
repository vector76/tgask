package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/vector76/tgask/internal/model"
	"github.com/vector76/tgask/internal/telegram"
)

var directCmd = &cobra.Command{
	Use:   "direct [prompt]",
	Short: "Send a prompt to Telegram directly and wait for a reply",
	RunE:  runDirect,
}

func init() {
	directCmd.Flags().StringP("file", "f", "", "Read prompt from file")
	directCmd.Flags().StringP("output", "o", "", "Write reply to file (stdout stays clean)")
}

func runDirect(cmd *cobra.Command, args []string) error {
	_ = godotenv.Load()
	api, err := newTgBotAdapter(os.Getenv("TGASK_BOT_TOKEN"))
	if err != nil {
		return fmt.Errorf("telegram init: %v", err)
	}
	code, err := doDirect(cmd, args, os.Stdin, os.Stdout, api)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// doDirect contains the testable core logic of the direct command.
// It returns an exit code (0 = success, 1 = error, 2 = expired) and any unexpected error.
func doDirect(cmd *cobra.Command, args []string, stdin io.Reader, stdout io.Writer, api telegram.BotAPI) (int, error) {
	botToken := os.Getenv("TGASK_BOT_TOKEN")
	if botToken == "" {
		fmt.Fprintln(os.Stderr, "error: TGASK_BOT_TOKEN is not set")
		return 1, nil
	}

	chatIDStr := os.Getenv("TGASK_CHAT_ID")
	if chatIDStr == "" {
		fmt.Fprintln(os.Stderr, "error: TGASK_CHAT_ID is not set")
		return 1, nil
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid TGASK_CHAT_ID: %v\n", err)
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

	job := model.NewJob(fmt.Sprintf("%d", time.Now().UnixNano()), prompt, time.Duration(timeoutSecs)*time.Second, false)

	tg := telegram.New(api, telegram.Config{BotToken: botToken, ChatID: chatID})
	tg.Start()

	if err := tg.SendQuery(job); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		tg.Stop()
		tg.Wait()
		return 1, nil
	}

	timer := time.NewTimer(job.Timeout)
	select {
	case reply := <-job.ReplyCh:
		timer.Stop()
		outputFile, _ := cmd.Flags().GetString("output")
		if outputFile != "" {
			if err := os.WriteFile(outputFile, []byte(reply), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing output file: %v\n", err)
				tg.Stop()
				tg.Wait()
				return 1, nil
			}
		} else {
			fmt.Fprintln(stdout, reply)
		}
		tg.Stop()
		tg.Wait()
		return 0, nil
	case <-timer.C:
		tg.HandleExpiry(job)
		tg.Stop()
		tg.Wait()
		fmt.Fprintln(os.Stderr, "error: query expired with no reply")
		return 2, nil
	}
}
