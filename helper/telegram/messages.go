package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

var (
	accessHashMu        sync.RWMutex
	channelAccessHashes = make(map[int64]int64)
	userAccessHashes    = make(map[int64]int64)
)

func CacheChannelAccessHash(channelID, hash int64) {
	if hash == 0 {
		return
	}
	accessHashMu.Lock()
	channelAccessHashes[channelID] = hash
	accessHashMu.Unlock()
}

func GetChannelAccessHash(channelID int64) int64 {
	accessHashMu.RLock()
	defer accessHashMu.RUnlock()
	return channelAccessHashes[channelID]
}

func CacheUserAccessHash(userID, hash int64) {
	if hash == 0 {
		return
	}
	accessHashMu.Lock()
	userAccessHashes[userID] = hash
	accessHashMu.Unlock()
}

func GetUserAccessHash(userID int64) int64 {
	accessHashMu.RLock()
	defer accessHashMu.RUnlock()
	return userAccessHashes[userID]
}

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

// ExtractMessageID extracts the message ID from any tg.UpdatesClass returned by SendMessage
func ExtractMessageID(updates tg.UpdatesClass) int {
	if updates == nil {
		return 0
	}
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		return u.ID
	case *tg.UpdateShortMessage:
		return u.ID
	case *tg.UpdateShortChatMessage:
		return u.ID
	case *tg.Updates:
		for _, upd := range u.Updates {
			switch m := upd.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
				if msg, ok := m.Message.(*tg.MessageService); ok {
					return msg.ID
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
				if msg, ok := m.Message.(*tg.MessageService); ok {
					return msg.ID
				}
			case *tg.UpdateMessageID:
				return m.ID
			}
		}
	case *tg.UpdatesCombined:
		for _, upd := range u.Updates {
			switch m := upd.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			}
		}
	}
	return 0
}

func (m *MessageSender) SendMessage(ctx context.Context, peer tg.InputPeerClass, text string, replyToID int, markup tg.ReplyMarkupClass) (int, error) {
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
		return 0, err
	}
	msgID := ExtractMessageID(updates)
	return msgID, nil
}

func (m *MessageSender) EditMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, text string, markup tg.ReplyMarkupClass) error {
	if msgID <= 0 {
		return nil
	}
	plainText, entities := ParseFormatting(text)
	req := &tg.MessagesEditMessageRequest{
		Peer:     peer,
		ID:       msgID,
		Message:  plainText,
		Entities: entities,
	}
	if markup != nil {
		req.ReplyMarkup = markup
	}
	_, err := m.rawClient.MessagesEditMessage(ctx, req)
	if err != nil {
		if strings.Contains(err.Error(), "MESSAGE_NOT_MODIFIED") || strings.Contains(err.Error(), "MESSAGE_ID_INVALID") || strings.Contains(err.Error(), "context canceled") {
			return nil
		}
		log.Printf("[AZML-Go Bot Client] Error editing message %d: %v", msgID, err)
	}
	return err
}

func PeerToChatID(peer tg.InputPeerClass) int64 {
	if peer == nil {
		return 0
	}
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return p.UserID
	case *tg.InputPeerChannel:
		if p.ChannelID > 0 {
			s := fmt.Sprintf("-100%d", p.ChannelID)
			val, _ := strconv.ParseInt(s, 10, 64)
			return val
		}
		return p.ChannelID
	case *tg.InputPeerChat:
		if p.ChatID > 0 {
			return -p.ChatID
		}
		return p.ChatID
	}
	return 0
}

var botAPIHTTPClient = &http.Client{Timeout: 8 * time.Second}

func (m *MessageSender) SendMessageWithButtons(ctx context.Context, peer tg.InputPeerClass, text string, replyToID int, btn *ButtonMaker, cols int) (int, error) {
	if btn == nil {
		return m.SendMessage(ctx, peer, text, replyToID, nil)
	}

	botToken := os.Getenv("BOT_TOKEN")
	chatID := PeerToChatID(peer)

	if btn.HasStyle() && botToken != "" && chatID != 0 {
		kbJSON := btn.BuildBotAPIKeyboard(cols)
		payload := map[string]interface{}{
			"chat_id":                  chatID,
			"text":                     text,
			"parse_mode":               "HTML",
			"reply_markup":             json.RawMessage(kbJSON),
			"disable_web_page_preview": true,
		}
		if replyToID != 0 {
			payload["reply_to_message_id"] = replyToID
		}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken), bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, rErr := botAPIHTTPClient.Do(req)
			if rErr == nil {
				defer resp.Body.Close()
				var res struct {
					Ok     bool `json:"ok"`
					Result struct {
						MessageID int `json:"message_id"`
					} `json:"result"`
				}
				if json.NewDecoder(resp.Body).Decode(&res) == nil && res.Ok {
					return res.Result.MessageID, nil
				}
			}
		}
	}

	return m.SendMessage(ctx, peer, text, replyToID, btn.BuildMenu(cols))
}

func (m *MessageSender) EditMessageWithButtons(ctx context.Context, peer tg.InputPeerClass, msgID int, text string, btn *ButtonMaker, cols int) error {
	if msgID <= 0 {
		return nil
	}
	if btn == nil {
		return m.EditMessage(ctx, peer, msgID, text, nil)
	}

	botToken := os.Getenv("BOT_TOKEN")
	chatID := PeerToChatID(peer)

	if btn.HasStyle() && botToken != "" && chatID != 0 {
		kbJSON := btn.BuildBotAPIKeyboard(cols)
		payload := map[string]interface{}{
			"chat_id":                  chatID,
			"message_id":               msgID,
			"text":                     text,
			"parse_mode":               "HTML",
			"reply_markup":             json.RawMessage(kbJSON),
			"disable_web_page_preview": true,
		}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", botToken), bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, rErr := botAPIHTTPClient.Do(req)
			if rErr == nil {
				defer resp.Body.Close()
				var res struct {
					Ok          bool   `json:"ok"`
					Description string `json:"description"`
				}
				if json.NewDecoder(resp.Body).Decode(&res) == nil {
					descLower := strings.ToLower(res.Description)
					if res.Ok || strings.Contains(descLower, "not modified") || strings.Contains(descLower, "message to edit not found") || strings.Contains(descLower, "message_id_invalid") {
						return nil
					}
					// If the message is a photo/media message, editMessageText fails; edit caption instead!
					if strings.Contains(descLower, "no text in the message") {
						capPayload := map[string]interface{}{
							"chat_id":      chatID,
							"message_id":   msgID,
							"caption":      text,
							"parse_mode":   "HTML",
							"reply_markup": json.RawMessage(kbJSON),
						}
						cBody, _ := json.Marshal(capPayload)
						cReq, cErr := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/editMessageCaption", botToken), bytes.NewReader(cBody))
						if cErr == nil {
							cReq.Header.Set("Content-Type", "application/json")
							cResp, crErr := botAPIHTTPClient.Do(cReq)
							if crErr == nil {
								cResp.Body.Close()
								return nil
							}
						}
					}
				}
			}
		}
	}

	return m.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(cols))
}

// DeleteMessage deletes messages for both PMs/basic groups and supergroups/channels
func (m *MessageSender) DeleteMessage(ctx context.Context, peer tg.InputPeerClass, msgIDs []int) error {
	if len(msgIDs) == 0 {
		return nil
	}
	var err error
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		accessHash := p.AccessHash
		if accessHash == 0 {
			accessHash = GetChannelAccessHash(p.ChannelID)
		}
		_, err = m.rawClient.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  p.ChannelID,
				AccessHash: accessHash,
			},
			ID: msgIDs,
		})
	default:
		_, err = m.rawClient.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     msgIDs,
		})
	}
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error deleting messages %v: %v", msgIDs, err)
	}
	return err
}

// SendPhotoFromURLWithButtons sends a photo from URL with inline buttons via Bot API or MTProto
func (m *MessageSender) SendPhotoFromURLWithButtons(ctx context.Context, peer tg.InputPeerClass, imageURL, caption string, replyToID int, btn *ButtonMaker, cols int) (int, error) {
	botToken := os.Getenv("BOT_TOKEN")
	chatID := PeerToChatID(peer)

	if botToken != "" && chatID != 0 {
		payload := map[string]interface{}{
			"chat_id":    chatID,
			"photo":      imageURL,
			"caption":    caption,
			"parse_mode": "HTML",
		}
		if btn != nil {
			payload["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
		}
		if replyToID != 0 {
			payload["reply_to_message_id"] = replyToID
		}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", botToken), bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, rErr := botAPIHTTPClient.Do(req)
			if rErr == nil {
				defer resp.Body.Close()
				var res struct {
					Ok     bool `json:"ok"`
					Result struct {
						MessageID int `json:"message_id"`
					} `json:"result"`
				}
				if json.NewDecoder(resp.Body).Decode(&res) == nil && res.Ok {
					return res.Result.MessageID, nil
				}
			}
		}
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bad HTTP status fetching photo: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	u := uploader.NewUploader(m.rawClient)
	inputFile, err := u.FromBytes(ctx, "cover.png", data)
	if err != nil {
		return 0, err
	}

	plainText, entities := ParseFormatting(caption)
	mediaReq := &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedPhoto{
			File: inputFile,
		},
		Message:  plainText,
		Entities: entities,
		RandomID: time.Now().UnixNano(),
	}
	if btn != nil {
		mediaReq.ReplyMarkup = btn.BuildMenu(cols)
	}
	if replyToID != 0 {
		mediaReq.ReplyTo = &tg.InputReplyToMessage{
			ReplyToMsgID: replyToID,
		}
	}

	updates, err := m.rawClient.MessagesSendMedia(ctx, mediaReq)
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error sending photo: %v", err)
		return 0, err
	}
	return ExtractMessageID(updates), nil
}

// SendPhotoFromURL fetches a photo from imageURL and sends it to Telegram as a native photo
func (m *MessageSender) SendPhotoFromURL(ctx context.Context, peer tg.InputPeerClass, imageURL, caption string, replyToID int) (int, error) {
	return m.SendPhotoFromURLWithButtons(ctx, peer, imageURL, caption, replyToID, nil, 1)
}

// SendLocalPhoto uploads a local photo file and sends it to Telegram as a native photo
func (m *MessageSender) SendLocalPhoto(ctx context.Context, peer tg.InputPeerClass, filePath, caption string, replyToID int) (int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, err
	}

	u := uploader.NewUploader(m.rawClient)
	inputFile, err := u.FromBytes(ctx, filepath.Base(filePath), data)
	if err != nil {
		return 0, err
	}

	plainText, entities := ParseFormatting(caption)
	mediaReq := &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedPhoto{
			File: inputFile,
		},
		Message:  plainText,
		Entities: entities,
		RandomID: time.Now().UnixNano(),
	}
	if replyToID != 0 {
		mediaReq.ReplyTo = &tg.InputReplyToMessage{
			ReplyToMsgID: replyToID,
		}
	}

	updates, err := m.rawClient.MessagesSendMedia(ctx, mediaReq)
	if err != nil {
		log.Printf("[AZML-Go Bot Client] Error sending local photo: %v", err)
		return 0, err
	}
	return ExtractMessageID(updates), nil
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

// FetchMessage retrieves a message by ID from a given peer (supports User, Chat, and Channel)
func FetchMessage(ctx context.Context, rawClient *tg.Client, peer tg.InputPeerClass, msgID int) (*tg.Message, error) {
	if rawClient == nil || msgID == 0 {
		return nil, fmt.Errorf("invalid client or message ID")
	}

	var res tg.MessagesMessagesClass
	var err error

	if pChan, ok := peer.(*tg.InputPeerChannel); ok {
		hash := pChan.AccessHash
		if hash == 0 {
			hash = GetChannelAccessHash(pChan.ChannelID)
		}
		if hash != 0 {
			res, err = rawClient.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
				Channel: &tg.InputChannel{ChannelID: pChan.ChannelID, AccessHash: hash},
				ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
			})
		}
		if err != nil || hash == 0 {
			res, err = rawClient.MessagesGetMessages(ctx, []tg.InputMessageClass{
				&tg.InputMessageID{ID: msgID},
			})
		}
	} else {
		res, err = rawClient.MessagesGetMessages(ctx, []tg.InputMessageClass{
			&tg.InputMessageID{ID: msgID},
		})
	}

	if err != nil {
		return nil, err
	}

	switch mRes := res.(type) {
	case *tg.MessagesMessages:
		for _, mClass := range mRes.Messages {
			if m, ok := mClass.(*tg.Message); ok {
				return m, nil
			}
		}
	case *tg.MessagesMessagesSlice:
		for _, mClass := range mRes.Messages {
			if m, ok := mClass.(*tg.Message); ok {
				return m, nil
			}
		}
	case *tg.MessagesChannelMessages:
		for _, mClass := range mRes.Messages {
			if m, ok := mClass.(*tg.Message); ok {
				return m, nil
			}
		}
	}

	return nil, fmt.Errorf("message not found")
}

