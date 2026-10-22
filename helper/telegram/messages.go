package telegram

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"
)

type MessageSender struct {
	rawClient *tg.Client
}

func NewMessageSender(rawClient *tg.Client) *MessageSender {
	return &MessageSender{
		rawClient: rawClient,
	}
}

// ParseFormatting converts HTML markup strings (<b>, <i>, <code>, <a href="...">) into pure text and Telegram entities
func ParseFormatting(text string) (string, []tg.MessageEntityClass) {
	if text == "" {
		return "", nil
	}
	var b entity.Builder
	if err := html.HTML(strings.NewReader(text), &b, html.Options{}); err != nil {
		return text, nil
	}
	return b.Complete()
}

func (m *MessageSender) SendMessage(ctx context.Context, peer tg.InputPeerClass, text string, replyToID int, markup tg.ReplyMarkupClass) (*tg.UpdatesClass, error) {
	plainText, entities := ParseFormatting(text)
	req := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  plainText,
		Entities: entities,
		RandomID: time.Now().UnixNano(),
	}
	if replyToID != 0 {
		req.ReplyTo = &tg.InputReplyToMessage{
			ReplyToMsgID: replyToID,
		}
	}
	if markup != nil {
		req.ReplyMarkup = markup
	}

	updates, err := m.rawClient.MessagesSendMessage(ctx, req)
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error sending message to peer %T: %v", peer, err)
		return nil, err
	}
	return &updates, nil
}

func (m *MessageSender) EditMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, text string, markup tg.ReplyMarkupClass) error {
	plainText, entities := ParseFormatting(text)
	req := &tg.MessagesEditMessageRequest{
		Peer:     peer,
		ID:      msgID,
		Message:  plainText,
		Entities: entities,
	}
	if markup != nil {
		req.ReplyMarkup = markup
	}
	_, err := m.rawClient.MessagesEditMessage(ctx, req)
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error editing message %d: %v", msgID, err)
	}
	return err
}

func (m *MessageSender) DeleteMessage(ctx context.Context, peer tg.InputPeerClass, msgIDs []int) error {
	_, err := m.rawClient.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
		ID: msgIDs,
	})
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error deleting messages %v: %v", msgIDs, err)
	}
	return err
}

func (m *MessageSender) AutoDeleteMessage(peer tg.InputPeerClass, msgID int, seconds int) {
	if seconds <= 0 {
		return
	}
	go func() {
		time.Sleep(time.Duration(seconds) * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.DeleteMessage(ctx, peer, []int{msgID})
	}()
}

func (m *MessageSender) AnswerCallback(ctx context.Context, queryID int64, text string, alert bool) error {
	_, err := m.rawClient.MessagesSetBotCallbackAnswer(ctx, &tg.MessagesSetBotCallbackAnswerRequest{
		QueryID: queryID,
		Message: text,
		Alert:   alert,
	})
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error answering callback %d: %v", queryID, err)
	}
	return err
}
