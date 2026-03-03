package telegram

// BotAPI abstracts the Telegram Bot API HTTP calls so the Telegram component
// can be tested without real network calls.
type BotAPI interface {
	// SendMessage sends a plain text message to the given chat.
	// replyMarkup may be nil for plain messages.
	// Returns the sent message's message_id.
	SendMessage(chatID int64, text string, replyMarkup interface{}) (messageID int, err error)

	// SendForceReplyMessage sends a message with ForceReply markup enabled,
	// prompting the recipient to reply directly to the message.
	// Returns the sent message's message_id.
	SendForceReplyMessage(chatID int64, text string) (messageID int, err error)

	// DeleteMessage deletes a previously sent message.
	DeleteMessage(chatID int64, messageID int) error

	// GetUpdates long-polls for new updates starting from offset.
	// timeout is the long-poll timeout in seconds (pass 30 for production).
	// allowedUpdates filters update types (pass []string{"message"} to receive only message updates).
	GetUpdates(offset int, timeout int, allowedUpdates []string) ([]Update, error)
}

// Update represents a single incoming update from Telegram.
type Update struct {
	UpdateID int
	Message  *Message
}

// Message represents a Telegram message.
type Message struct {
	MessageID      int
	Text           string
	ReplyToMessage *Message // non-nil when this message is a reply to another message
}
