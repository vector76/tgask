package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/vector76/tgask/internal/model"
	"github.com/vector76/tgask/internal/queue"
	"github.com/vector76/tgask/internal/server"
	"github.com/vector76/tgask/internal/telegram"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the tgask HTTP server",
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	// 1. Load .env (ignore file-not-found; fail on parse errors)
	_ = godotenv.Load()

	// 2. Read and validate required env vars
	botToken := os.Getenv("TGASK_BOT_TOKEN")
	chatIDStr := os.Getenv("TGASK_CHAT_ID")
	token := os.Getenv("TGASK_TOKEN")
	port := os.Getenv("TGASK_PORT")
	for _, pair := range [][]string{{"TGASK_BOT_TOKEN", botToken}, {"TGASK_CHAT_ID", chatIDStr}, {"TGASK_TOKEN", token}, {"TGASK_PORT", port}} {
		if pair[1] == "" {
			return fmt.Errorf("required environment variable %s is not set", pair[0])
		}
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("TGASK_CHAT_ID must be an integer: %v", err)
	}

	// 3. Build components
	tgClient, err := newTgBotAdapter(botToken)
	if err != nil {
		return fmt.Errorf("telegram init: %v", err)
	}
	tg := telegram.New(tgClient, telegram.Config{BotToken: botToken, ChatID: chatID})

	// DispatchFunc wraps SendQuery (which returns error) into func(*model.Job)
	dispatch := func(job *model.Job) {
		if err := tg.SendQuery(job); err != nil {
			log.Printf("serve: SendQuery error: %v", err)
		}
	}
	q := queue.New(dispatch, tg.HandleExpiry)

	srv := server.New(server.Config{Token: token, Version: rootCmd.Version}, q, tg)

	// 4. Start background workers
	q.Start()
	tg.Start()

	// 5. Start HTTP server
	httpSrv := &http.Server{Addr: ":" + port, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("serve: listening on :%s", port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: http error: %v", err)
		}
	}()

	// 6. Wait for signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	// 7. Graceful shutdown
	tg.Stop()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutCtx)
}
