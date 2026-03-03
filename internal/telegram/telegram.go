package telegram

import (
	"fmt"
	"log"
	"sync"

	"github.com/vector76/tgask/internal/model"
)

type Config struct {
	BotToken string
	ChatID   int64
}

type Telegram struct {
	api        BotAPI
	cfg        Config
	inFlight   map[int]*model.Job // keyed by TelegramMessageID
	inFlightMu sync.Mutex         // protects inFlight
	offset     int                // only accessed by pollLoop goroutine; no mutex needed
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func New(api BotAPI, cfg Config) *Telegram {
	return &Telegram{
		api:      api,
		cfg:      cfg,
		inFlight: make(map[int]*model.Job),
		stopCh:   make(chan struct{}),
	}
}

func (t *Telegram) Start() {
	t.wg.Add(1)
	go t.pollLoop()
}

func (t *Telegram) Stop() {
	close(t.stopCh)
}

// Wait blocks until the poll loop goroutine has exited.
func (t *Telegram) Wait() {
	t.wg.Wait()
}

func (t *Telegram) pollLoop() {
	defer t.wg.Done()
	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		updates, err := t.api.GetUpdates(t.offset, 30, []string{"message"})
		if err != nil {
			log.Printf("telegram: GetUpdates error: %v", err)
			continue
		}

		for _, update := range updates {
			if update.Message != nil && update.Message.ReplyToMessage != nil {
				t.handleReply(update.Message)
			}
			t.offset = update.UpdateID + 1
		}
	}
}

func (t *Telegram) SendQuery(job *model.Job) error {
	text := fmt.Sprintf("%s\n\nReply by %s", job.Prompt, job.ExpiresAt.Format("15:04"))

	messageID, err := t.api.SendForceReplyMessage(t.cfg.ChatID, text)
	if err != nil {
		return err
	}

	job.TelegramMessageID = messageID
	job.Status = model.StatusAwaitingReply

	t.inFlightMu.Lock()
	t.inFlight[messageID] = job
	t.inFlightMu.Unlock()

	return nil
}

func (t *Telegram) SendNotification(text string) error {
	_, err := t.api.SendMessage(t.cfg.ChatID, text, nil)
	return err
}

func (t *Telegram) HandleExpiry(job *model.Job) {
	t.inFlightMu.Lock()
	delete(t.inFlight, job.TelegramMessageID)
	t.inFlightMu.Unlock()

	if err := t.api.DeleteMessage(t.cfg.ChatID, job.TelegramMessageID); err != nil {
		log.Printf("telegram: DeleteMessage error: %v", err)
	}
	if _, err := t.api.SendMessage(t.cfg.ChatID, "This query expired — no response needed.", nil); err != nil {
		log.Printf("telegram: SendMessage (expiry notice) error: %v", err)
	}
}

func (t *Telegram) handleReply(msg *Message) {
	t.inFlightMu.Lock()
	job, ok := t.inFlight[msg.ReplyToMessage.MessageID]
	if ok {
		delete(t.inFlight, msg.ReplyToMessage.MessageID)
	}
	t.inFlightMu.Unlock() // unlock BEFORE channel send

	if !ok {
		return // silently discard non-matching replies
	}

	// Non-blocking send: if the channel already has a value, discard the duplicate
	select {
	case job.ReplyCh <- msg.Text:
	default:
	}
}
