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

	"azml-go/config"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var (
	accessHashMu        sync.RWMutex
	channelAccessHashes = make(map[int64]int64)
	userAccessHashes    = make(map[int64]int64)

	dumpForwardMu   sync.Mutex
	lastDumpForward time.Time
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

// ResolveChannelAccessHash attempts to fetch and cache the AccessHash for a channel if not already cached.
func ResolveChannelAccessHash(ctx context.Context, client *tg.Client, chatID int64) int64 {
	raw := chatID
	if raw < 0 {
		rawStr := fmt.Sprintf("%d", raw)
		if strings.HasPrefix(rawStr, "-100") {
			if id, err := strconv.ParseInt(rawStr[4:], 10, 64); err == nil {
				raw = id
			}
		} else if strings.HasPrefix(rawStr, "-") {
			if id, err := strconv.ParseInt(rawStr[1:], 10, 64); err == nil {
				raw = id
			}
		}
	}
	if hash := GetChannelAccessHash(raw); hash != 0 {
		return hash
	}
	if client == nil {
		return 0
	}

	// 1. Try MessagesGetChats first (does not require AccessHash)
	if chats, err := client.MessagesGetChats(ctx, []int64{raw}); err == nil && chats != nil {
		var chatList []tg.ChatClass
		switch c := chats.(type) {
		case *tg.MessagesChats:
			chatList = c.Chats
		case *tg.MessagesChatsSlice:
			chatList = c.Chats
		}
		for _, chat := range chatList {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == raw && ch.AccessHash != 0 {
				CacheChannelAccessHash(raw, ch.AccessHash)
				return ch.AccessHash
			}
		}
	}

	// 2. Try ChannelsGetChannels as fallback
	chats, err := client.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{
			ChannelID:  raw,
			AccessHash: 0,
		},
	})
	if err == nil && chats != nil {
		var chatList []tg.ChatClass
		switch c := chats.(type) {
		case *tg.MessagesChats:
			chatList = c.Chats
		case *tg.MessagesChatsSlice:
			chatList = c.Chats
		}
		for _, chat := range chatList {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == raw && ch.AccessHash != 0 {
				CacheChannelAccessHash(raw, ch.AccessHash)
				return ch.AccessHash
			}
		}
	}

	return 0
}

// ForwardToChannelWithFloodWait safely forwards a message to a channel with rate limiting
// (minimum 1500ms between forwards) and automatic retry on Telegram FLOOD_WAIT errors.
// This prevents Telegram from flagging the bot session when multiple files are forwarded concurrently.
func ForwardToChannelWithFloodWait(ctx context.Context, client *tg.Client, fromPeer tg.InputPeerClass, toPeer tg.InputPeerClass, msgID int) (int, error) {
	dumpForwardMu.Lock()
	defer dumpForwardMu.Unlock()

	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		since := time.Since(lastDumpForward)
		if since < 1500*time.Millisecond {
			delay := 1500*time.Millisecond - since
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		lastDumpForward = time.Now()

		fwdRes, err := client.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
			FromPeer:   fromPeer,
			ToPeer:     toPeer,
			ID:         []int{msgID},
			RandomID:   []int64{time.Now().UnixNano()},
			DropAuthor: true,
		})

		if err != nil {
			if flood, ok := tgerr.AsFloodWait(err); ok {
				waitTime := flood + 1*time.Second
				log.Printf("[Dump Forward] FLOOD_WAIT: sleeping %v before retry (attempt %d/%d)", waitTime, attempt+1, maxRetries)
				select {
				case <-time.After(waitTime):
					continue
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}
			return 0, fmt.Errorf("failed to forward message to channel: %w", err)
		}

		fwdMsgID := ExtractMessageID(fwdRes)
		if fwdMsgID > 0 {
			return fwdMsgID, nil
		}
		return 0, fmt.Errorf("failed to extract forwarded message ID from response")
	}

	return 0, fmt.Errorf("exceeded max retries forwarding message to channel")
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

// parseFloodWait returns true and the required duration if the error is a Telegram FLOOD_WAIT error
func parseFloodWait(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return d, true
	}
	errStr := err.Error()
	if strings.Contains(errStr, "FLOOD_WAIT_") {
		parts := strings.Split(errStr, "FLOOD_WAIT_")
		if len(parts) > 1 {
			var secs int
			for _, ch := range parts[1] {
				if ch >= '0' && ch <= '9' {
					secs = secs*10 + int(ch-'0')
				} else {
					break
				}
			}
			if secs > 0 {
				return time.Duration(secs) * time.Second, true
			}
		}
	}
	return 0, false
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

	for attempt := 0; attempt < 5; attempt++ {
		req.RandomID = time.Now().UnixNano()
		updates, err := m.rawClient.MessagesSendMessage(ctx, req)
		if err == nil {
			return ExtractMessageID(updates), nil
		}
		if d, ok := parseFloodWait(err); ok {
			if d > 60*time.Second {
				d = 60 * time.Second
			}
			select {
			case <-time.After(d + 200*time.Millisecond):
				continue
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		if attempt == 4 {
			log.Printf("[AZML-Go Bot Client] Error sending message to peer %T: %v", peer, err)
		}
		return 0, err
	}
	return 0, fmt.Errorf("failed to send message after retries")
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

	for attempt := 0; attempt < 5; attempt++ {
		_, err := m.rawClient.MessagesEditMessage(ctx, req)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "MESSAGE_NOT_MODIFIED") || strings.Contains(err.Error(), "MESSAGE_ID_INVALID") || strings.Contains(err.Error(), "context canceled") {
			return nil
		}
		if d, ok := parseFloodWait(err); ok {
			if d > 60*time.Second {
				d = 60 * time.Second
			}
			select {
			case <-time.After(d + 200*time.Millisecond):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if attempt == 4 {
			log.Printf("[AZML-Go Bot Client] Error editing message %d: %v", msgID, err)
		}
		return err
	}
	return nil
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

var botAPIHTTPClient = &http.Client{Timeout: 5 * time.Second}

func getBotToken() string {
	if cfg := config.Get(); cfg != nil && cfg.BotToken != "" {
		return cfg.BotToken
	}
	return os.Getenv("BOT_TOKEN")
}

func (m *MessageSender) SendMessageWithButtons(ctx context.Context, peer tg.InputPeerClass, text string, replyToID int, btn *ButtonMaker, cols int) (int, error) {
	if btn == nil {
		return m.SendMessage(ctx, peer, text, replyToID, nil)
	}

	botToken := getBotToken()
	chatID := PeerToChatID(peer)
	if botToken != "" && chatID != 0 && btn.HasStyle() {
		msgID, err := m.sendBotAPIMessage(ctx, botToken, chatID, text, replyToID, btn, cols)
		if err == nil && msgID > 0 {
			return msgID, nil
		}
		if err != nil {
			log.Printf("[AZML-Go Bot API] SendMessage failed: %v, falling back to MTProto", err)
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

	botToken := getBotToken()
	chatID := PeerToChatID(peer)
	if botToken != "" && chatID != 0 && btn.HasStyle() {
		err := m.editBotAPIMessage(ctx, botToken, chatID, msgID, text, btn, cols)
		if err == nil {
			return nil
		}
		if err != nil {
			log.Printf("[AZML-Go Bot API] EditMessage failed: %v, falling back to MTProto", err)
		}
	}

	return m.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(cols))
}

func (m *MessageSender) sendBotAPIMessage(ctx context.Context, botToken string, chatID int64, text string, replyToID int, btn *ButtonMaker, cols int) (int, error) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if replyToID != 0 {
		payload["reply_to_message_id"] = replyToID
	}
	if btn != nil {
		payload["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := botAPIHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return 0, err
	}
	if !res.Ok {
		return 0, fmt.Errorf("bot api error: %s", res.Description)
	}
	return res.Result.MessageID, nil
}

func (m *MessageSender) editBotAPIMessage(ctx context.Context, botToken string, chatID int64, msgID int, text string, btn *ButtonMaker, cols int) error {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": msgID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if btn != nil {
		payload["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", botToken), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := botAPIHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if res.Ok || strings.Contains(res.Description, "message is not modified") {
		return nil
	}

	// If the message is a photo/media message, edit caption instead
	if strings.Contains(res.Description, "there is no text in the message to edit") {
		capPayload := map[string]interface{}{
			"chat_id":    chatID,
			"message_id": msgID,
			"caption":    text,
			"parse_mode": "HTML",
		}
		if btn != nil {
			capPayload["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
		}
		capBody, _ := json.Marshal(capPayload)
		capReq, cErr := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/editMessageCaption", botToken), bytes.NewReader(capBody))
		if cErr == nil {
			capReq.Header.Set("Content-Type", "application/json")
			if capResp, doErr := botAPIHTTPClient.Do(capReq); doErr == nil {
				defer capResp.Body.Close()
				var capRes struct {
					Ok          bool   `json:"ok"`
					Description string `json:"description"`
				}
				if json.NewDecoder(capResp.Body).Decode(&capRes) == nil && (capRes.Ok || strings.Contains(capRes.Description, "message is not modified")) {
					return nil
				}
			}
		}
	}

	return fmt.Errorf("bot api error: %s", res.Description)
}

// DeleteMessage deletes messages for both PMs/basic groups and supergroups/channels
func (m *MessageSender) DeleteMessage(ctx context.Context, peer tg.InputPeerClass, msgIDs []int) error {
	if len(msgIDs) == 0 {
		return nil
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
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
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "MESSAGE_ID_INVALID") || strings.Contains(err.Error(), "context canceled") {
			return nil
		}
		if d, ok := parseFloodWait(err); ok {
			if d > 15*time.Second {
				d = 15 * time.Second
			}
			select {
			case <-time.After(d + 200*time.Millisecond):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		break
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
		if hash == 0 {
			return nil, fmt.Errorf("channel access hash unknown for %d", pChan.ChannelID)
		}
		res, err = rawClient.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: pChan.ChannelID, AccessHash: hash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
		})
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
