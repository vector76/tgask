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

func init() {
	serveCmd.Flags().String("token", "", "HTTP bearer token (overrides TGASK_TOKEN)")
}

type serveConfig struct {
	botToken   string
	chatID     int64
	token      string
	port       string
	jobTimeout time.Duration
}

func resolveServeConfig(cmd *cobra.Command) (serveConfig, error) {
	_ = godotenv.Load()

	botToken := os.Getenv("TGASK_BOT_TOKEN")
	chatIDStr := os.Getenv("TGASK_CHAT_ID")
	port := os.Getenv("TGASK_PORT")

	token, _ := cmd.Flags().GetString("token")
	if token == "" {
		token = os.Getenv("TGASK_TOKEN")
	}
	if token == "" {
		return serveConfig{}, fmt.Errorf("token not set: use --token flag or TGASK_TOKEN env var")
	}

	for _, pair := range [][]string{{"TGASK_BOT_TOKEN", botToken}, {"TGASK_CHAT_ID", chatIDStr}, {"TGASK_PORT", port}} {
		if pair[1] == "" {
			return serveConfig{}, fmt.Errorf("required environment variable %s is not set", pair[0])
		}
	}

	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return serveConfig{}, fmt.Errorf("TGASK_CHAT_ID must be an integer: %v", err)
	}

	jobTimeout := 3600 * time.Second
	if s := os.Getenv("TGASK_JOB_TIMEOUT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			jobTimeout = time.Duration(n) * time.Second
		}
	}

	return serveConfig{botToken: botToken, chatID: chatID, token: token, port: port, jobTimeout: jobTimeout}, nil
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := resolveServeConfig(cmd)
	if err != nil {
		return err
	}

	// 3. Build components
	tgClient, err := newTgBotAdapter(cfg.botToken)
	if err != nil {
		return fmt.Errorf("telegram init: %v", err)
	}
	tg := telegram.New(tgClient, telegram.Config{BotToken: cfg.botToken, ChatID: cfg.chatID})

	// DispatchFunc wraps SendQuery (which returns error) into func(*model.Job)
	dispatch := func(job *model.Job) {
		if err := tg.SendQuery(job); err != nil {
			log.Printf("serve: SendQuery error: %v", err)
		}
	}
	q := queue.New(dispatch, tg.HandleExpiry)

	srv := server.New(server.Config{Token: cfg.token, Version: rootCmd.Version, DefaultJobTimeout: cfg.jobTimeout}, q, tg)

	// 4. Start background workers
	q.Start()
	tg.Start()

	// 5. Start HTTP server
	httpSrv := &http.Server{Addr: ":" + cfg.port, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("serve: listening on :%s", cfg.port)
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
