package telegram

import (
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
