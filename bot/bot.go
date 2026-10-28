package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"
	"azml-go/helper/uploader"
	"azml-go/types"

	td "github.com/gotd/td/telegram"
	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

var (
	accessHashMu        sync.RWMutex
	userAccessHashes    = make(map[int64]int64)
	channelAccessHashes = make(map[int64]int64)
)

type BotApp struct {
	client      *td.Client
	rawAPI      *tg.Client
	userClient  *td.Client
	userRawAPI  *tg.Client
	dispatcher  tg.UpdateDispatcher
	gaps        *updates.Manager
	sender      *telegram.MessageSender
	uploader    *uploader.TelegramUploader
	userMap     *types.SafeUserMap
	sessPool    *pool.SessionPool
	startTime   time.Time
	botUsername string

	// Modules
	leechHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool)
	ytdlHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statusHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statusCbHandler       func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	cancelHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cancelAllHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cancelCbHandler       func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	settingsHandler       func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	autoRenameHandler     func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	settingsCbHandler     func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	settingsUpdateHandler func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, u *types.UserSettings, key string)
	bsettingHandler       func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	bsettingCbHandler     func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	bsettingInputHandler  func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key, val string, page int)
	bsettingCancelHandler func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key string, page int)
	authHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	unauthHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	addSudoHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmSudoHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	addPremHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmPremHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	checkPremHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	startHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	helpHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	pingHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statsHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	logHandler            func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	restartHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	speedtestHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	mediaInfoHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	broadcastHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	imdbHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	animeHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)

	statsCbHandler      func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	megaSelectCbHandler func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	gdSelectCbHandler   func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
}

type botUpdateHandler struct {
	app        *BotApp
	dispatcher tg.UpdateDispatcher
	gaps       *updates.Manager
}

func (h *botUpdateHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	switch upd := u.(type) {
	case *tg.UpdateShortMessage:
		msg := &tg.Message{
			Out:       upd.Out,
			Mentioned: upd.Mentioned,
			ID:        upd.ID,
			PeerID:    &tg.PeerUser{UserID: upd.UserID},
			FromID:    &tg.PeerUser{UserID: upd.UserID},
			Message:   upd.Message,
			Date:      upd.Date,
			ReplyTo:   upd.ReplyTo,
			Entities:  upd.Entities,
		}
		h.app.routeMessage(ctx, msg, tg.Entities{
			Users: map[int64]*tg.User{
				upd.UserID: {
					ID: upd.UserID,
				},
			},
		})
		return nil

	case *tg.UpdateShortChatMessage:
		msg := &tg.Message{
			Out:       upd.Out,
			Mentioned: upd.Mentioned,
			ID:        upd.ID,
			PeerID:    &tg.PeerChat{ChatID: upd.ChatID},
			FromID:    &tg.PeerUser{UserID: upd.FromID},
			Message:   upd.Message,
			Date:      upd.Date,
			ReplyTo:   upd.ReplyTo,
			Entities:  upd.Entities,
		}
		h.app.routeMessage(ctx, msg, tg.Entities{
			Users: map[int64]*tg.User{
				upd.FromID: {
					ID: upd.FromID,
				},
			},
		})
		return nil

	default:
		switch upd := u.(type) {
		case *tg.Updates:
			for _, chat := range upd.Chats {
				if ch, ok := chat.(*tg.Channel); ok && ch.AccessHash != 0 {
					h.app.cacheChannelAccessHash(ch.ID, ch.AccessHash)
				}
			}
			for _, user := range upd.Users {
				if usr, ok := user.(*tg.User); ok && usr.AccessHash != 0 {
					h.app.cacheAccessHash(usr.ID, usr.AccessHash)
				}
			}
		case *tg.UpdatesCombined:
			for _, chat := range upd.Chats {
				if ch, ok := chat.(*tg.Channel); ok && ch.AccessHash != 0 {
					h.app.cacheChannelAccessHash(ch.ID, ch.AccessHash)
				}
			}
			for _, user := range upd.Users {
				if usr, ok := user.(*tg.User); ok && usr.AccessHash != 0 {
					h.app.cacheAccessHash(usr.ID, usr.AccessHash)
				}
			}
		}
		return h.gaps.Handle(ctx, u)
	}
}

func NewBotApp() *BotApp {
	cfg := config.Get()
	userMap := types.NewSafeUserMap()

	dispatcher := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{
		Handler: dispatcher,
	})

	app := &BotApp{
		dispatcher: dispatcher,
		gaps:       gaps,
		userMap:    userMap,
		startTime:  time.Now(),
	}

	handler := &botUpdateHandler{
		app:        app,
		dispatcher: dispatcher,
		gaps:       gaps,
	}

	client := td.NewClient(cfg.TelegramAPI, cfg.TelegramHash, td.Options{
		UpdateHandler: handler,
	})
	app.client = client
	app.rawAPI = client.API()

	var userClient *td.Client
	var userRawAPI *tg.Client
	if cfg.UserSessionString != "" {
		if uStorage, err := telegram.ParseSessionString(context.Background(), cfg.UserSessionString); err == nil {
			userClient = td.NewClient(cfg.TelegramAPI, cfg.TelegramHash, td.Options{
				SessionStorage: uStorage,
			})
			userRawAPI = userClient.API()
		} else {
			log.Printf("[AZML-Go User Client] Failed to parse USER_SESSION_STRING: %v", err)
		}
	}
	app.userClient = userClient
	app.userRawAPI = userRawAPI

	app.sender = telegram.NewMessageSender(app.rawAPI)
	app.uploader = uploader.NewTelegramUploader(app.rawAPI, app.userRawAPI)

	app.registerDispatcher()
	return app
}

func (b *BotApp) SetLeechHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool)) {
	b.leechHandler = h
}
func (b *BotApp) SetYtdlHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.ytdlHandler = h
}
func (b *BotApp) SetStatusHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.statusHandler = h
	b.statusCbHandler = cb
}
func (b *BotApp) SetCancelHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), all func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.cancelHandler = h
	b.cancelAllHandler = all
	b.cancelCbHandler = cb
}
func (b *BotApp) SetSettingsHandlers(
	h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext),
	cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64),
	update func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, u *types.UserSettings, key string),
) {
	b.settingsHandler = h
	b.settingsCbHandler = cb
	b.settingsUpdateHandler = update
}
func (b *BotApp) SetAutoRenameHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.autoRenameHandler = h
}
func (b *BotApp) SetBotSettingsHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.bsettingHandler = h
	b.bsettingCbHandler = cb
}
func (b *BotApp) SetBotSettingsInputHandlers(val func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key, val string, page int), cancel func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key string, page int)) {
	b.bsettingInputHandler = val
	b.bsettingCancelHandler = cancel
}
func (b *BotApp) SetAuthorizeHandlers(auth, unauth, addSudo, rmSudo, addPrem, rmPrem, checkPrem func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.authHandler = auth
	b.unauthHandler = unauth
	b.addSudoHandler = addSudo
	b.rmSudoHandler = rmSudo
	b.addPremHandler = addPrem
	b.rmPremHandler = rmPrem
	b.checkPremHandler = checkPrem
}
func (b *BotApp) SetHelpHandlers(
	start, help, ping, stats, logH, restart func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext),
	statsCb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64),
) {
	b.startHandler = start
	b.helpHandler = help
	b.pingHandler = ping
	b.statsHandler = stats
	b.logHandler = logH
	b.restartHandler = restart
	b.statsCbHandler = statsCb
}
func (b *BotApp) SetExtraHandlers(speed, mi, bc, imdb, anime func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.speedtestHandler = speed
	b.mediaInfoHandler = mi
	b.broadcastHandler = bc
	b.imdbHandler = imdb
	b.animeHandler = anime
}
func (b *BotApp) SetSelectHandlers(mega, gd func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.megaSelectCbHandler = mega
	b.gdSelectCbHandler = gd
}
func (b *BotApp) GetRawAPI() *tg.Client                   { return b.rawAPI }
func (b *BotApp) GetUserRawAPI() *tg.Client               { return b.userRawAPI }
func (b *BotApp) GetSender() *telegram.MessageSender      { return b.sender }
func (b *BotApp) GetUploader() *uploader.TelegramUploader { return b.uploader }
func (b *BotApp) GetUserMap() *types.SafeUserMap          { return b.userMap }
func (b *BotApp) GetStartTime() time.Time                 { return b.startTime }
func (b *BotApp) SetSessionPool(p *pool.SessionPool)      { b.sessPool = p }
func (b *BotApp) GetSessionPool() *pool.SessionPool       { return b.sessPool }

func (b *BotApp) registerDispatcher() {
	b.dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewMessage) error {
		msg, ok := update.Message.(*tg.Message)
		if !ok || msg.Out {
			return nil
		}
		go b.routeMessage(ctx, msg, e)
		return nil
	})

	b.dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {
		for chID, ch := range e.Channels {
			if ch.AccessHash != 0 {
				b.cacheChannelAccessHash(chID, ch.AccessHash)
			}
		}
		msg, ok := update.Message.(*tg.Message)
		if !ok || msg.Out {
			return nil
		}
		go b.routeMessage(ctx, msg, e)
		return nil
	})

	b.dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, update *tg.UpdateBotCallbackQuery) error {
		for chID, ch := range e.Channels {
			if ch.AccessHash != 0 {
				b.cacheChannelAccessHash(chID, ch.AccessHash)
			}
		}
		for uID, u := range e.Users {
			if u.AccessHash != 0 {
				b.cacheAccessHash(uID, u.AccessHash)
			}
		}
		go b.routeCallback(ctx, update, e)
		return nil
	})
}

func (b *BotApp) routeMessage(ctx context.Context, m *tg.Message, entities tg.Entities) {
	peer := b.getPeerWithEntities(m.PeerID, entities)
	if peer == nil {
		log.Printf("[AZML-Go Bot Client] Could not resolve peer for msg %d", m.ID)
		return
	}

	chatID := getPeerID(m.PeerID)
	senderID := chatID
	senderName := "User"
	senderUsername := ""

	if m.FromID != nil {
		if uPeer, ok := m.FromID.(*tg.PeerUser); ok {
			senderID = uPeer.UserID
			if uObj, exists := entities.Users[senderID]; exists {
				senderName = uObj.FirstName
				senderUsername = uObj.Username
				b.cacheAccessHash(senderID, uObj.AccessHash)
			}
		}
	} else if uPeer, ok := m.PeerID.(*tg.PeerUser); ok {
		senderID = uPeer.UserID
		if uObj, exists := entities.Users[senderID]; exists {
			senderName = uObj.FirstName
			senderUsername = uObj.Username
			b.cacheAccessHash(senderID, uObj.AccessHash)
		}
	}

	text := strings.TrimSpace(m.Message)

	// Automatically record user has started/interacted in PM
	if chatID == senderID {
		u := b.userMap.GetOrCreate(senderID)
		if !u.StartedInPM {
			u.StartedInPM = true
			b.userMap.Set(u)
			go func(usr *types.UserSettings) {
				_ = ext.DB.SaveUser(usr)
			}(u)
		}
	}

	// Check if user is in an active pending setting prompt session (e.g. from /us)
	if sess, ok := ext.GetPendingSettingSession(senderID, chatID); ok && sess != nil {
		inMsgID := m.ID
		if text == "/cancel" {
			ext.ClearPendingSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			if sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
				u := b.userMap.GetOrCreate(senderID)
				b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, u, sess.Key)
			}
			return
		}
		if !strings.HasPrefix(text, "/") {
			ext.ClearPendingSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			u := b.userMap.GetOrCreate(senderID)
			b.applyUserSettingValue(ctx, peer, u, sess.Key, text, sess.PromptMsgID)
			return
		}
	}

	// Check if user is in an active pending bot setting prompt session (from /bs)
	if bSess, ok := ext.GetPendingBotSettingSession(senderID, chatID); ok && bSess != nil {
		inMsgID := m.ID
		if text == "/cancel" {
			ext.ClearPendingBotSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			if b.bsettingCancelHandler != nil {
				b.bsettingCancelHandler(ctx, peer, bSess.PromptMsgID, bSess.Key, bSess.Page)
			}
			return
		}
		if !strings.HasPrefix(text, "/") {
			ext.ClearPendingBotSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			if b.bsettingInputHandler != nil {
				b.bsettingInputHandler(ctx, peer, bSess.PromptMsgID, bSess.Key, text, bSess.Page)
			}
			return
		}
	}

	if sess, ok := ext.GetPendingThumbSession(senderID, chatID); ok && text == "/cancel" {
		inMsgID := m.ID
		ext.ClearPendingThumbSession(senderID)
		go func() {
			delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
		}()
		if sess != nil && sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
			u := b.userMap.GetOrCreate(senderID)
			b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, u, "thumb")
		}
		return
	}

	// 1. Handle incoming private media attachments (token.pickle, config.env, cookies.txt, accounts.zip, thumbnails)
	if m.Media != nil {
		if b.handleIncomingMedia(ctx, peer, m, senderID, chatID) {
			return
		}
		if text == "" {
			return
		}
	}

	// 2. ONLY respond to bot commands! Ignore all normal chatting and other bots' messages completely.
	if !telegram.BotCommands.IsAnyCommand(text, b.botUsername) {
		return
	}

	msgCtx := &types.MessageContext{
		ChatID:     chatID,
		MessageID:  m.ID,
		UserID:     senderID,
		SenderName: senderName,
		Username:   senderUsername,
		Text:       text,
		HasMedia:   m.Media != nil,
		RawMedia:   m.Media,
	}

	if m.ReplyTo != nil {
		if header, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			msgCtx.IsReply = true
			msgCtx.ReplyToID = header.ReplyToMsgID

			// 1. Direct media attached to reply header if provided by Telegram
			if header.ReplyMedia != nil {
				msgCtx.HasMedia = true
				msgCtx.RawMedia = header.ReplyMedia
			}

			// 2. Fetch replied message directly from this chat with generous timeout (8s) so concurrent tasks never miss media
			if msgCtx.RawMedia == nil {
				fetchCtx, fetchCancel := context.WithTimeout(ctx, 8*time.Second)
				var repliedMsg *tg.Message
			fetchLoop:
				for attempt := 0; attempt < 3; attempt++ {
					var err error
					repliedMsg, err = telegram.FetchMessage(fetchCtx, b.rawAPI, peer, msgCtx.ReplyToID)
					if err == nil && repliedMsg != nil {
						break
					}
					select {
					case <-fetchCtx.Done():
						break fetchLoop
					case <-time.After(250 * time.Millisecond):
					}
				}
				if repliedMsg != nil {
					msgCtx.ReplyText = repliedMsg.Message
					if repliedMsg.Media != nil {
						msgCtx.HasMedia = true
						msgCtx.RawMedia = repliedMsg.Media
					}
				}
				fetchCancel()
			}
		}
	}

	// 3. PUBLIC COMMANDS: /start, /ping, /help (Always answered regardless of authorization)
	if telegram.MatchCommand(text, telegram.BotCommands.Start, b.botUsername) {
		if b.startHandler != nil {
			b.startHandler(ctx, peer, msgCtx)
		}
		return
	}
	if telegram.MatchCommand(text, telegram.BotCommands.Ping, b.botUsername) {
		if b.pingHandler != nil {
			b.pingHandler(ctx, peer, msgCtx)
		}
		return
	}
	if telegram.MatchCommand(text, telegram.BotCommands.Help, b.botUsername) {
		if b.helpHandler != nil {
			b.helpHandler(ctx, peer, msgCtx)
		}
		return
	}

	// 4. Blacklist check
	if telegram.IsBlacklisted(senderID, b.userMap) {
		return
	}

	// 5. Authorization check for leech, admin, settings, and task commands
	if !telegram.IsAuthorized(chatID, senderID, b.userMap) {
		log.Printf("[AZML-Go] Unauthorized command %q by user %d in chat %d", text, senderID, chatID)
		sentID, _ := b.sender.SendMessage(ctx, peer, "⌬ <b>Access Denied!</b>\nYou are not authorized to use this bot.\nContact the bot owner to get authorization.", m.ID, nil)
		if sentID > 0 {
			switch m.PeerID.(type) {
			case *tg.PeerChat, *tg.PeerChannel:
				b.sender.AutoDeleteMessage(peer, sentID, 15)
			}
		}
		return
	}

	// 6. Bot PM Check (AZML style): If command is executed in a group, verify user has started the bot in PM
	isOwnerOrSudo := telegram.IsSudo(senderID, b.userMap)
	isGroupChat := chatID != senderID
	switch m.PeerID.(type) {
	case *tg.PeerChat, *tg.PeerChannel:
		isGroupChat = true
	}

	if !isOwnerOrSudo && isGroupChat {
		u := b.userMap.Get(senderID)
		if u == nil || !u.StartedInPM {
			botUser := b.botUsername
			if botUser == "" {
				botUser = telegram.CurrentBotUsername
			}
			btn := telegram.NewButtonMaker()
			btn.UButtonStyled("◈ "+telegram.ToSmallCaps("Start Bot in PM"), fmt.Sprintf("https://t.me/%s?start=start", botUser), "primary")
			sentID, err := b.sender.SendMessage(ctx, peer, "<i>You didn't START the bot in PM (Private)</i>", m.ID, btn.BuildMenu(1))
			if err == nil && sentID > 0 {
				b.sender.AutoDeleteMessage(peer, sentID, 20)
			}
			return
		}
	}

	isCancel, _ := telegram.MatchCancelCommand(text, b.botUsername)

	// Dispatch commands - all check against current bot username
	switch {
	case telegram.MatchCommand(text, telegram.BotCommands.Leech, b.botUsername):
		isZip := strings.Contains(strings.ToLower(text), "zip") && !strings.Contains(strings.ToLower(text), "unzip")
		isUnzip := strings.Contains(strings.ToLower(text), "unzip")
		if b.leechHandler != nil {
			b.leechHandler(ctx, peer, msgCtx, isZip, isUnzip)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Ytdl, b.botUsername):
		if b.ytdlHandler != nil {
			b.ytdlHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Status, b.botUsername):
		if b.statusHandler != nil {
			b.statusHandler(ctx, peer, msgCtx)
		}
	case isCancel:
		if b.cancelHandler != nil {
			b.cancelHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.CancelAll, b.botUsername):
		if b.cancelAllHandler != nil {
			b.cancelAllHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.UserSetting, b.botUsername):
		if b.settingsHandler != nil {
			b.settingsHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AutoRename, b.botUsername):
		if b.autoRenameHandler != nil {
			b.autoRenameHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.BotSetting, b.botUsername):
		if b.bsettingHandler != nil {
			b.bsettingHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Authorize, b.botUsername):
		if b.authHandler != nil {
			b.authHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.UnAuthorize, b.botUsername):
		if b.unauthHandler != nil {
			b.unauthHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AddSudo, b.botUsername):
		if b.addSudoHandler != nil {
			b.addSudoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmSudo, b.botUsername):
		if b.rmSudoHandler != nil {
			b.rmSudoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AddPremium, b.botUsername):
		if b.addPremHandler != nil {
			b.addPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmPremium, b.botUsername):
		if b.rmPremHandler != nil {
			b.rmPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Premium, b.botUsername):
		if b.checkPremHandler != nil {
			b.checkPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Help, b.botUsername):
		if b.helpHandler != nil {
			b.helpHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Ping, b.botUsername):
		if b.pingHandler != nil {
			b.pingHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Stats, b.botUsername):
		if b.statsHandler != nil {
			b.statsHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Log, b.botUsername):
		if b.logHandler != nil {
			b.logHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Restart, b.botUsername):
		if b.restartHandler != nil {
			b.restartHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Speedtest, b.botUsername):
		if b.speedtestHandler != nil {
			b.speedtestHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.MediaInfo, b.botUsername):
		if b.mediaInfoHandler != nil {
			b.mediaInfoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Broadcast, b.botUsername):
		if b.broadcastHandler != nil {
			b.broadcastHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Imdb, b.botUsername):
		if b.imdbHandler != nil {
			b.imdbHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Anime, b.botUsername):
		if b.animeHandler != nil {
			b.animeHandler(ctx, peer, msgCtx)
		}
	}
}

func (b *BotApp) routeCallback(ctx context.Context, cb *tg.UpdateBotCallbackQuery, entities tg.Entities) {
	data := string(cb.Data)
	peer := b.getPeerWithEntities(cb.Peer, entities)
	if peer == nil {
		return
	}

	switch {
	case strings.HasPrefix(data, "stats") || strings.HasPrefix(data, "wzmlx") || strings.HasPrefix(data, "start_"):
		if b.statsCbHandler != nil {
			b.statsCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "status"):
		if b.statusCbHandler != nil {
			b.statusCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "canall"):
		if b.cancelCbHandler != nil {
			b.cancelCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "uset") || strings.HasPrefix(data, "userset"):
		if b.settingsCbHandler != nil {
			b.settingsCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "botset"):
		if b.bsettingCbHandler != nil {
			b.bsettingCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "megasel"):
		if b.megaSelectCbHandler != nil {
			b.megaSelectCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "gdsel"):
		if b.gdSelectCbHandler != nil {
			b.gdSelectCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	}
}

func (b *BotApp) Start(ctx context.Context) error {
	cfg := config.Get()

	// Start user session client if configured
	if b.userClient != nil {
		go func() {
			log.Println("[AZML-Go User Client] Starting user session client...")
			_ = b.userClient.Run(ctx, func(uCtx context.Context) error {
				status, err := b.userClient.Auth().Status(uCtx)
				if err != nil {
					log.Printf("[AZML-Go User Client] Auth status check error: %v", err)
					return err
				}
				if status.Authorized {
					userSelf, _ := b.userClient.Self(uCtx)
					if userSelf != nil {
						uname := userSelf.Username
						if uname == "" {
							uname = userSelf.FirstName
						}
						isPrem := userSelf.Premium
						config.SetIsPremiumUser(isPrem)
						if isPrem {
							log.Printf("[AZML-Go User Client] Logged in successfully with User Session as @%s (ID: %d, PREMIUM ACTIVE: Max Split 3.91GB / 4GB upload enabled)", uname, userSelf.ID)
						} else {
							log.Printf("[AZML-Go User Client] Logged in successfully with User Session as @%s (ID: %d, NON-PREMIUM: Max Split 1.91GB)", uname, userSelf.ID)
						}
					} else {
						config.SetIsPremiumUser(false)
						log.Println("[AZML-Go User Client] Logged in successfully with User Session (NON-PREMIUM: Max Split 1.91GB)")
					}
				} else {
					config.SetIsPremiumUser(false)
					log.Println("[AZML-Go User Client] Session authorized status false (Max Split 1.91GB)")
				}
				<-uCtx.Done()
				return uCtx.Err()
			})
		}()
	} else {
		config.SetIsPremiumUser(false)
		log.Println("[AZML-Go User Client] No user session string configured (Max Split 1.91GB / 2GB upload mode active)")
	}

	log.Println("[AZML-Go Bot Client] Starting bot client...")
	return b.client.Run(ctx, func(ctx context.Context) error {
		// Log in as Bot
		status, err := b.client.Auth().Status(ctx)
		if err != nil {
			return err
		}

		if !status.Authorized {
			if _, err := b.client.Auth().Bot(ctx, cfg.BotToken); err != nil {
				return fmt.Errorf("bot authentication failed: %v", err)
			}
		}

		botSelf, _ := b.client.Self(ctx)
		if botSelf != nil {
			b.botUsername = botSelf.Username
			telegram.SetBotUsername(botSelf.Username)
			uname := botSelf.Username
			if uname == "" {
				uname = botSelf.FirstName
			}
			log.Printf("[AZML-Go Bot Client] Logged in successfully with Bot Token as @%s (ID: %d)", uname, botSelf.ID)
		} else {
			log.Println("[AZML-Go Bot Client] Logged in successfully with Bot Token")
		}

		// Register all bot commands in Telegram's autocomplete list
		b.registerBotCommands(ctx)

		// Start updates manager in background
		if botSelf != nil {
			go func() {
				log.Printf("[AZML-Go Bot Client] Starting updates manager for bot user ID %d...", botSelf.ID)
				if err := b.gaps.Run(ctx, b.rawAPI, botSelf.ID, updates.AuthOptions{IsBot: true}); err != nil && err != context.Canceled {
					log.Printf("[AZML-Go Bot Client] Updates manager stopped: %v", err)
				}
			}()
		}

		// Ensure Aria2c is up with 16 connections
		_ = ext.DB.LoadAllUsers(b.userMap)
		log.Println("[AZML-Go Bot Client] AZML-Go started and listening for commands!")

		// Trigger startup notification
		go b.sendStartupNotification(ctx)

		<-ctx.Done()
		return ctx.Err()
	})
}

func (b *BotApp) sendStartupNotification(ctx context.Context) {
	time.Sleep(1 * time.Second)
	cfg := config.Get()
	now := time.Now()
	dateStr := now.Format("02/01/06")
	timeStr := now.Format("03:04:05 PM")
	timz := "Asia/Kolkata"

	// 1. Check if .restartmsg exists
	restartData, err := os.ReadFile(".restartmsg")
	if err == nil && len(restartData) > 0 {
		_ = os.Remove(".restartmsg")
		parts := strings.Fields(string(restartData))
		if len(parts) >= 2 {
			chatID, _ := strconv.ParseInt(parts[0], 10, 64)
			msgID, _ := strconv.Atoi(parts[1])
			if chatID != 0 && msgID != 0 {
				restartSuccessText := fmt.Sprintf("⌬ <b><i>Restarted Successfully!</i></b>\n"+
					"┠ <b>Date:</b> %s\n"+
					"┠ <b>Time:</b> %s\n"+
					"┠ <b>TimeZone:</b> %s\n"+
					"┖ <b>Version:</b> <code>1.0.0</code>", dateStr, timeStr, timz)

				botToken := cfg.BotToken
				if botToken != "" {
					url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", botToken)
					payload := map[string]interface{}{
						"chat_id":    chatID,
						"message_id": msgID,
						"text":       restartSuccessText,
						"parse_mode": "HTML",
					}
					body, _ := json.Marshal(payload)
					_, _ = http.Post(url, "application/json", bytes.NewReader(body))
				}
				return
			}
		}
	}

	// 2. Otherwise send Bot Started Successfully notification to Authorized Chats (GC) or Owner
	startMsg := fmt.Sprintf("⌬ <b><i>Bot Started Successfully!</i></b>\n"+
		"┠ <b>Date:</b> %s\n"+
		"┠ <b>Time:</b> %s\n"+
		"┠ <b>TimeZone:</b> %s\n"+
		"┖ <b>Version:</b> <code>1.0.0</code>", dateStr, timeStr, timz)

	botToken := cfg.BotToken
	if botToken == "" {
		botToken = os.Getenv("BOT_TOKEN")
	}
	if botToken == "" {
		log.Println("[AZML-Go Bot Client] Warning: BOT_TOKEN is empty, cannot send startup notification via Bot API")
		return
	}

	sendViaBotAPI := func(targetID int64) {
		if targetID == 0 {
			return
		}
		url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
		payload := map[string]interface{}{
			"chat_id":                  targetID,
			"text":                     startMsg,
			"parse_mode":               "HTML",
			"disable_web_page_preview": true,
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("[AZML-Go Bot Client] Error sending startup message to %d: %v", targetID, err)
			return
		}
		defer resp.Body.Close()
		var res struct {
			Ok          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if json.NewDecoder(resp.Body).Decode(&res) == nil {
			if res.Ok {
				log.Printf("[AZML-Go Bot Client] Startup notification sent to %d successfully!", targetID)
			} else {
				log.Printf("[AZML-Go Bot Client] Failed to send startup notification to %d: %s", targetID, res.Description)
			}
		}
	}

	log.Printf("[AZML-Go Bot Client] Triggering startup notification...")
	sentToAny := false
	for _, chatID := range cfg.AuthorizedChats {
		if chatID != 0 {
			log.Printf("[AZML-Go Bot Client] Sending startup notification to Authorized Group/Chat: %d", chatID)
			sendViaBotAPI(chatID)
			sentToAny = true
		}
	}
	if !sentToAny && cfg.OwnerID != 0 {
		log.Printf("[AZML-Go Bot Client] No authorized chats configured; sending startup notification to Owner: %d", cfg.OwnerID)
		sendViaBotAPI(cfg.OwnerID)
	}
}

func (b *BotApp) registerBotCommands(ctx context.Context) {
	cfg := config.Get()
	cmds := make([]tg.BotCommand, 0)

	addCmd := func(cmd, desc string) {
		clean := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(cmd, "/")))
		if clean == "" {
			return
		}
		if len(clean) > 32 {
			clean = clean[:32]
		}
		if len(desc) < 3 {
			desc = desc + " command"
		}
		if len(desc) > 256 {
			desc = desc[:256]
		}
		cmds = append(cmds, tg.BotCommand{
			Command:     clean,
			Description: desc,
		})
	}

	bc := telegram.BotCommands
	if len(bc.Leech) > 1 {
		addCmd(bc.Leech[0], fmt.Sprintf("or /%s Leech [links/media/rclone_path]", bc.Leech[1]))
	} else if len(bc.Leech) > 0 {
		addCmd(bc.Leech[0], "Leech [links/media/rclone_path]")
	}

	if cfg.ShowExtraCmds && len(bc.Leech) >= 6 {
		addCmd(bc.Leech[2], fmt.Sprintf("or /%s Leech and Zip [links/media]", bc.Leech[3]))
		addCmd(bc.Leech[4], fmt.Sprintf("or /%s Leech and Unzip [links/media]", bc.Leech[5]))
	}

	if len(bc.Ytdl) > 1 {
		addCmd(bc.Ytdl[0], fmt.Sprintf("or /%s Leech yt-dlp supported links", bc.Ytdl[1]))
	}
	if len(bc.Status) > 1 {
		addCmd(bc.Status[0], fmt.Sprintf("or /%s Get Bot All Status Stats Message", bc.Status[1]))
	}
	if len(bc.Stats) > 1 {
		addCmd(bc.Stats[0], fmt.Sprintf("or /%s Check Bot & System stats", bc.Stats[1]))
	}
	if len(bc.Cancel) > 0 {
		addCmd(bc.Cancel[0], "Cancel a Task of yours!")
	}
	if len(bc.CancelAll) > 0 {
		addCmd(bc.CancelAll[0], "Cancel all Tasks in whole Bots.")
	}
	if len(bc.Help) > 0 {
		addCmd(bc.Help[0], "Get detailed help about the Bot")
	}
	if len(bc.UserSetting) > 1 {
		addCmd(bc.UserSetting[0], fmt.Sprintf("or /%s User's Personal Settings (Open in PM)", bc.UserSetting[1]))
	}
	if len(bc.BotSetting) > 1 {
		addCmd(bc.BotSetting[0], fmt.Sprintf("or /%s Bot Settings (Owner/Sudo)", bc.BotSetting[1]))
	}
	if len(bc.Speedtest) > 1 {
		addCmd(bc.Speedtest[0], fmt.Sprintf("or /%s Check Server Up & Down Speed", bc.Speedtest[1]))
	}
	if len(bc.MediaInfo) > 1 {
		addCmd(bc.MediaInfo[0], fmt.Sprintf("or /%s Generate Mediainfo for Media or DL links", bc.MediaInfo[1]))
	}
	if len(bc.Broadcast) > 1 {
		addCmd(bc.Broadcast[0], fmt.Sprintf("or /%s Broadcast message to all users", bc.Broadcast[1]))
	}
	if len(bc.Imdb) > 0 {
		addCmd(bc.Imdb[0], "Search Movies/Series on IMDB.com")
	}
	if len(bc.Anime) > 1 {
		addCmd(bc.Anime[0], fmt.Sprintf("or /%s Search Animes on AniList.com", bc.Anime[1]))
	}
	if len(bc.Ping) > 1 {
		addCmd(bc.Ping[0], fmt.Sprintf("or /%s Check Bot Ping & Latency", bc.Ping[1]))
	}
	if len(bc.Restart) > 1 {
		addCmd(bc.Restart[0], fmt.Sprintf("or /%s Restart the Bot (Owner/Sudo)", bc.Restart[1]))
	}
	if len(bc.Log) > 0 {
		addCmd(bc.Log[0], "Get Bot Log (Owner/Sudo)")
	}

	req := &tg.BotsSetBotCommandsRequest{
		Scope:    &tg.BotCommandScopeDefault{},
		LangCode: "",
		Commands: cmds,
	}

	if ok, err := b.rawAPI.BotsSetBotCommands(ctx, req); err != nil {
		log.Printf("[AZML-Go Bot Client] Failed to register bot commands with Telegram: %v", err)
	} else {
		log.Printf("[AZML-Go Bot Client] Successfully registered %d commands in bot command list (ok=%v)", len(cmds), ok)
	}
}

func (b *BotApp) cacheAccessHash(userID, hash int64) {
	if hash == 0 {
		return
	}
	accessHashMu.Lock()
	userAccessHashes[userID] = hash
	accessHashMu.Unlock()
	telegram.CacheUserAccessHash(userID, hash)
}

func (b *BotApp) cacheChannelAccessHash(channelID, hash int64) {
	if hash == 0 {
		return
	}
	accessHashMu.Lock()
	channelAccessHashes[channelID] = hash
	accessHashMu.Unlock()
	telegram.CacheChannelAccessHash(channelID, hash)
}

func (b *BotApp) getPeerWithEntities(p tg.PeerClass, entities tg.Entities) tg.InputPeerClass {
	switch v := p.(type) {
	case *tg.PeerUser:
		if u, ok := entities.Users[v.UserID]; ok && u.AccessHash != 0 {
			b.cacheAccessHash(u.ID, u.AccessHash)
			return u.AsInputPeer()
		}
		accessHashMu.RLock()
		hash := userAccessHashes[v.UserID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetUserAccessHash(v.UserID)
		}
		return &tg.InputPeerUser{UserID: v.UserID, AccessHash: hash}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: v.ChatID}
	case *tg.PeerChannel:
		if ch, ok := entities.Channels[v.ChannelID]; ok && ch.AccessHash != 0 {
			b.cacheChannelAccessHash(ch.ID, ch.AccessHash)
			return ch.AsInputPeer()
		}
		accessHashMu.RLock()
		hash := channelAccessHashes[v.ChannelID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetChannelAccessHash(v.ChannelID)
		}
		return &tg.InputPeerChannel{ChannelID: v.ChannelID, AccessHash: hash}
	}
	return nil
}

func getPeer(p tg.PeerClass) tg.InputPeerClass {
	switch v := p.(type) {
	case *tg.PeerUser:
		accessHashMu.RLock()
		hash := userAccessHashes[v.UserID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetUserAccessHash(v.UserID)
		}
		return &tg.InputPeerUser{UserID: v.UserID, AccessHash: hash}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: v.ChatID}
	case *tg.PeerChannel:
		accessHashMu.RLock()
		hash := channelAccessHashes[v.ChannelID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetChannelAccessHash(v.ChannelID)
		}
		return &tg.InputPeerChannel{ChannelID: v.ChannelID, AccessHash: hash}
	}
	return nil
}

func getPeerID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return v.ChatID
	case *tg.PeerChannel:
		return v.ChannelID
	}
	return 0
}

func (b *BotApp) getPeerForChatID(chatID int64) tg.InputPeerClass {
	if chatID > 0 {
		accessHashMu.RLock()
		hash := userAccessHashes[chatID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetUserAccessHash(chatID)
		}
		return &tg.InputPeerUser{UserID: chatID, AccessHash: hash}
	}
	rawChanID := pool.RawChannelID(chatID)
	accessHashMu.RLock()
	hash := channelAccessHashes[rawChanID]
	accessHashMu.RUnlock()
	if hash == 0 {
		hash = telegram.GetChannelAccessHash(rawChanID)
	}
	return &tg.InputPeerChannel{ChannelID: rawChanID, AccessHash: hash}
}

func (b *BotApp) handleIncomingMedia(ctx context.Context, peer tg.InputPeerClass, m *tg.Message, senderID, chatID int64) bool {
	// Case 1: Document media
	if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := docMedia.Document.(*tg.Document); ok {
			fileName := ""
			for _, attr := range doc.Attributes {
				if fnAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
					fileName = fnAttr.FileName
					break
				}
			}
			lowerName := strings.ToLower(fileName)

			// 1A. Private files for Owner / Sudo
			switch lowerName {
			case "token.pickle":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>token.pickle</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "token.pickle")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>token.pickle</b>: %v", err), m.ID, nil)
					return true
				}
				// Reload Google Drive OAuth credentials immediately
				gClient := downloader.GetGDriveClient()
				if gClient != nil {
					_ = gClient.LoadCredentials("token.pickle")
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>token.pickle</b> received and saved! Google Drive OAuth credentials successfully reloaded.", m.ID, nil)
				return true

			case "config.env":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>config.env</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "config.env")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>config.env</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = config.LoadConfig("config.env")
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>config.env</b> received and saved! Configurations reloaded.", m.ID, nil)
				return true

			case "cookies.txt":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>cookies.txt</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "cookies.txt")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>cookies.txt</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>cookies.txt</b> received and saved for yt-dlp & aria2c!", m.ID, nil)
				return true

			case "accounts.zip":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>accounts.zip</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "accounts.zip")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>accounts.zip</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>accounts.zip</b> received and saved successfully!", m.ID, nil)
				return true

			case "shorteners.txt":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>shorteners.txt</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "shorteners.txt")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>shorteners.txt</b>: %v", err), m.ID, nil)
					return true
				}
				ext.LoadShorteners()
				_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>shorteners.txt</b> received and saved! %d shortener(s) loaded.", len(ext.GetShorteners())), m.ID, nil)
				return true
			}

			// 1B. Custom Thumbnail document
			isImageDoc := strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") || strings.HasSuffix(lowerName, ".png") || strings.HasSuffix(lowerName, ".webp") || strings.HasPrefix(doc.MimeType, "image/")
			if isImageDoc {
				caption := strings.ToLower(strings.TrimSpace(m.Message))
				isThumbCmd := strings.HasPrefix(caption, "/us thumb") || strings.HasPrefix(caption, "/setthumb") || strings.HasPrefix(caption, "/usersetting thumb") || strings.HasPrefix(caption, "/t") || strings.HasPrefix(caption, "/thumb")
				sess, isPending := ext.GetPendingThumbSession(senderID, chatID)
				if isThumbCmd || (isPending && sess != nil && !strings.HasPrefix(caption, "/")) {
					ext.ClearPendingThumbSession(senderID)
					_ = os.MkdirAll("Thumbnails", 0755)
					destThumb := fmt.Sprintf("Thumbnails/%d.jpg", senderID)
					dl := tdDownloader.NewDownloader()
					loc := doc.AsInputDocumentFileLocation("")
					if loc != nil {
						_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, destThumb)
						if err == nil {
							u := b.userMap.GetOrCreate(senderID)
							u.CustomThumb = destThumb
							_ = ext.DB.SaveUser(u)

							// Always send immediate confirmation to user in current chat
							_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>Custom thumbnail saved!</b> It will be attached to all your future leeched files.", m.ID, nil)

							// Refresh interactive /us settings menu if prompt was active
							if sess != nil && sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
								settingsPeer := peer
								if sess.ChatID != 0 && sess.ChatID != chatID {
									if p := b.getPeerForChatID(sess.ChatID); p != nil {
										settingsPeer = p
									}
								}
								b.settingsUpdateHandler(ctx, settingsPeer, sess.PromptMsgID, u, "thumb")
							}
							return true
						}
					}
				}
				return false
			}

			// 1C. Torrent file document
			if strings.HasSuffix(lowerName, ".torrent") {
				cfg := config.Get()
				_ = os.MkdirAll(cfg.DownloadDir, 0755)
				torrentPath := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s.torrent", senderID, ext.RandomHex(4)))
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc != nil {
					_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, torrentPath)
					if err == nil && b.leechHandler != nil {
						msgCtx := &types.MessageContext{
							ChatID:     chatID,
							MessageID:  m.ID,
							UserID:     senderID,
							SenderName: "User",
							Text:       fmt.Sprintf("/leech %s", torrentPath),
						}
						b.leechHandler(ctx, peer, msgCtx, false, false)
						return true
					}
				}
			}
		}
	}

	// Case 2: Photo media (Custom thumbnail for user)
	if photoMedia, ok := m.Media.(*tg.MessageMediaPhoto); ok {
		caption := strings.ToLower(strings.TrimSpace(m.Message))
		isThumbCmd := strings.HasPrefix(caption, "/us thumb") || strings.HasPrefix(caption, "/setthumb") || strings.HasPrefix(caption, "/usersetting thumb") || strings.HasPrefix(caption, "/t") || strings.HasPrefix(caption, "/thumb")
		sess, isPending := ext.GetPendingThumbSession(senderID, chatID)
		if isThumbCmd || (isPending && sess != nil && !strings.HasPrefix(caption, "/")) {
			ext.ClearPendingThumbSession(senderID)
			if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
				_ = os.MkdirAll("Thumbnails", 0755)
				destThumb := fmt.Sprintf("Thumbnails/%d.jpg", senderID)
				thumbSize := "m"
				if len(photo.Sizes) > 0 {
					switch s := photo.Sizes[len(photo.Sizes)-1].(type) {
					case *tg.PhotoSize:
						thumbSize = s.Type
					case *tg.PhotoSizeProgressive:
						thumbSize = s.Type
					}
				}
				dl := tdDownloader.NewDownloader()
				loc := photo.AsInputPhotoFileLocation(thumbSize)
				if loc != nil {
					_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, destThumb)
					if err == nil {
						u := b.userMap.GetOrCreate(senderID)
						u.CustomThumb = destThumb
						_ = ext.DB.SaveUser(u)

						// Always send immediate confirmation to user in current chat
						_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>Custom thumbnail saved!</b> It will be attached to all your future leeched files.", m.ID, nil)

						// Refresh interactive /us settings menu if prompt was active
						if sess != nil && sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
							settingsPeer := peer
							if sess.ChatID != 0 && sess.ChatID != chatID {
								if p := b.getPeerForChatID(sess.ChatID); p != nil {
									settingsPeer = p
								}
							}
							b.settingsUpdateHandler(ctx, settingsPeer, sess.PromptMsgID, u, "thumb")
						}
						return true
					}
				}
			}
		}
		return false
	}

	return false
}

func (b *BotApp) applyUserSettingValue(ctx context.Context, peer tg.InputPeerClass, u *types.UserSettings, key, val string, promptMsgID int) {
	val = strings.TrimSpace(val)
	if val == "" {
		return
	}
	var fieldName string
	switch key {
	case "lprefix":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.Prefix = ""
			val = "Cleared"
		} else {
			u.Prefix = val
		}
		fieldName = "Leech Prefix"
	case "lsuffix":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.Suffix = ""
			val = "Cleared"
		} else {
			u.Suffix = val
		}
		fieldName = "Leech Suffix"
	case "lcaption":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") || strings.EqualFold(val, "default") {
			u.Caption = ""
			val = "Default"
		} else {
			u.Caption = val
		}
		fieldName = "Leech Caption"
	case "lremname_auto":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.RemnameAuto = ""
			val = "Cleared"
		} else {
			u.RemnameAuto = val
		}
		fieldName = "AutoRename Template"
	case "lremname_regex":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.RemnameRegex = ""
			val = "Cleared"
		} else {
			u.RemnameRegex = val
		}
		fieldName = "Regex Remname"
	case "ldump":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.DumpChatID = 0
			val = "Cleared"
		} else {
			id, _ := strconv.ParseInt(val, 10, 64)
			u.DumpChatID = id
		}
		fieldName = "Leech Dump Channel"
	case "lmeta":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			if u.Metadata != nil {
				delete(u.Metadata, "all")
			}
			val = "Cleared"
		} else {
			if u.Metadata == nil {
				u.Metadata = make(map[string]string)
			}
			u.Metadata["all"] = val
		}
		fieldName = "Leech Metadata"
	case "split_size":
		sizeStr := strings.ToUpper(val)
		maxSplit := config.GetMaxSplitSize()
		var bytes int64
		if strings.HasSuffix(sizeStr, "GB") {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "GB"), 64)
			bytes = int64(f * 1024 * 1024 * 1024)
		} else if strings.HasSuffix(sizeStr, "MB") {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "MB"), 64)
			bytes = int64(f * 1024 * 1024)
		} else {
			bytes, _ = strconv.ParseInt(sizeStr, 10, 64)
		}
		if bytes > maxSplit || bytes <= 0 {
			bytes = maxSplit
		}
		u.SplitSize = bytes
		fieldName = "Leech Split Size"
		val = ext.GetReadableFileSize(u.SplitSize)
	}

	_ = ext.DB.SaveUser(u)
	if promptMsgID > 0 && b.settingsUpdateHandler != nil {
		b.settingsUpdateHandler(ctx, peer, promptMsgID, u, key)
	} else {
		confirmText := fmt.Sprintf("✓ <b>%s</b> updated to: <code>%s</code>", fieldName, val)
		_, _ = b.sender.SendMessage(ctx, peer, confirmText, 0, nil)
	}
}
