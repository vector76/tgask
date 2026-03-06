package cmd

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vector76/tgask/internal/telegram"
)

type tgBotAdapter struct {
	bot *tgbotapi.BotAPI
}

func newTgBotAdapter(token string) (*tgBotAdapter, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	return &tgBotAdapter{bot: bot}, nil
}

// SendMessage implements telegram.BotAPI
func (a *tgBotAdapter) SendMessage(chatID int64, text string, replyMarkup interface{}) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if replyMarkup != nil {
		msg.ReplyMarkup = replyMarkup
	}
	sent, err := a.bot.Send(msg)
	return sent.MessageID, err
}

// SendForceReplyMessage implements telegram.BotAPI
func (a *tgBotAdapter) SendForceReplyMessage(chatID int64, text string, parseMode string) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true, Selective: true}
	sent, err := a.bot.Send(msg)
	return sent.MessageID, err
}

// DeleteMessage implements telegram.BotAPI
func (a *tgBotAdapter) DeleteMessage(chatID int64, messageID int) error {
	_, err := a.bot.Request(tgbotapi.DeleteMessageConfig{ChatID: chatID, MessageID: messageID})
	return err
}

// GetUpdates implements telegram.BotAPI
func (a *tgBotAdapter) GetUpdates(offset, timeout int, allowedUpdates []string) ([]telegram.Update, error) {
	cfg := tgbotapi.UpdateConfig{Offset: offset, Timeout: timeout}
	cfg.AllowedUpdates = allowedUpdates
	updates, err := a.bot.GetUpdates(cfg)
	if err != nil {
		return nil, err
	}
	result := make([]telegram.Update, 0, len(updates))
	for _, u := range updates {
		tu := telegram.Update{UpdateID: u.UpdateID}
		if u.Message != nil {
			tu.Message = &telegram.Message{
				MessageID: u.Message.MessageID,
				Text:      u.Message.Text,
			}
			if u.Message.ReplyToMessage != nil {
				tu.Message.ReplyToMessage = &telegram.Message{
					MessageID: u.Message.ReplyToMessage.MessageID,
				}
			}
		}
		result = append(result, tu)
	}
	return result, nil
}
