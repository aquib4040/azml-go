package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
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
	client     *td.Client
	rawAPI     *tg.Client
	userClient *td.Client
	userRawAPI *tg.Client
	dispatcher tg.UpdateDispatcher
	gaps       *updates.Manager
	sender     *telegram.MessageSender
	uploader   *uploader.TelegramUploader
	userMap    *types.SafeUserMap
	startTime  time.Time
	botUsername string

	// Modules
	leechHandler     func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool)
	ytdlHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statusHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statusCbHandler  func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string)
	cancelHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cancelAllHandler func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cancelCbHandler  func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	settingsHandler   func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	settingsCbHandler func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	bsettingHandler   func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	bsettingCbHandler func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	authHandler       func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	unauthHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	addSudoHandler   func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmSudoHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	addPremHandler   func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmPremHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	checkPremHandler func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	startHandler     func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	helpHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	pingHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statsHandler     func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	logHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	restartHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	speedtestHandler  func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	mediaInfoHandler  func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	broadcastHandler  func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	imdbHandler       func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	animeHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)

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
func (b *BotApp) SetStatusHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string)) {
	b.statusHandler = h
	b.statusCbHandler = cb
}
func (b *BotApp) SetCancelHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), all func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.cancelHandler = h
	b.cancelAllHandler = all
	b.cancelCbHandler = cb
}
func (b *BotApp) SetSettingsHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.settingsHandler = h
	b.settingsCbHandler = cb
}
func (b *BotApp) SetBotSettingsHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.bsettingHandler = h
	b.bsettingCbHandler = cb
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
func (b *BotApp) GetSender() *telegram.MessageSender      { return b.sender }
func (b *BotApp) GetUploader() *uploader.TelegramUploader { return b.uploader }
func (b *BotApp) GetUserMap() *types.SafeUserMap          { return b.userMap }
func (b *BotApp) GetStartTime() time.Time                 { return b.startTime }

func (b *BotApp) registerDispatcher() {
	b.dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewMessage) error {
		msg, ok := update.Message.(*tg.Message)
		if !ok || msg.Out {
			return nil
		}
		b.routeMessage(ctx, msg, e)
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
		b.routeMessage(ctx, msg, e)
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
		b.routeCallback(ctx, update, e)
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

	if m.FromID != nil {
		if uPeer, ok := m.FromID.(*tg.PeerUser); ok {
			senderID = uPeer.UserID
			if uObj, exists := entities.Users[senderID]; exists {
				senderName = uObj.FirstName
				b.cacheAccessHash(senderID, uObj.AccessHash)
			}
		}
	} else if uPeer, ok := m.PeerID.(*tg.PeerUser); ok {
		senderID = uPeer.UserID
		if uObj, exists := entities.Users[senderID]; exists {
			senderName = uObj.FirstName
			b.cacheAccessHash(senderID, uObj.AccessHash)
		}
	}

	text := strings.TrimSpace(m.Message)

	// 1. Handle incoming private media attachments (token.pickle, config.env, cookies.txt, accounts.zip)
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

	log.Printf("[AZML-Go] Command %q from user %d in chat %d", text, senderID, chatID)

	msgCtx := &types.MessageContext{
		ChatID:     chatID,
		MessageID:  m.ID,
		UserID:     senderID,
		SenderName: senderName,
		Text:       text,
	}

	if m.ReplyTo != nil {
		if header, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			msgCtx.IsReply = true
			msgCtx.ReplyToID = header.ReplyToMsgID
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
		sentID, _ := b.sender.SendMessage(ctx, peer, "⚠️ <b>Access Denied!</b>\nYou are not authorized to use this bot.\nContact the bot owner to get authorization.", m.ID, nil)
		if sentID > 0 {
			switch m.PeerID.(type) {
			case *tg.PeerChat, *tg.PeerChannel:
				b.sender.AutoDeleteMessage(peer, sentID, 15)
			}
		}
		return
	}

	isCancel, _ := telegram.MatchCancelCommand(text)

	// Dispatch commands
	switch {
	case telegram.MatchCommand(text, telegram.BotCommands.Leech):
		isZip := strings.Contains(strings.ToLower(text), "zip") && !strings.Contains(strings.ToLower(text), "unzip")
		isUnzip := strings.Contains(strings.ToLower(text), "unzip")
		if b.leechHandler != nil {
			b.leechHandler(ctx, peer, msgCtx, isZip, isUnzip)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Ytdl):
		if b.ytdlHandler != nil {
			b.ytdlHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Status):
		if b.statusHandler != nil {
			b.statusHandler(ctx, peer, msgCtx)
		}
	case isCancel:
		if b.cancelHandler != nil {
			b.cancelHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.CancelAll):
		if b.cancelAllHandler != nil {
			b.cancelAllHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.UserSetting):
		if b.settingsHandler != nil {
			b.settingsHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.BotSetting):
		if b.bsettingHandler != nil {
			b.bsettingHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Authorize):
		if b.authHandler != nil {
			b.authHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.UnAuthorize):
		if b.unauthHandler != nil {
			b.unauthHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AddSudo):
		if b.addSudoHandler != nil {
			b.addSudoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmSudo):
		if b.rmSudoHandler != nil {
			b.rmSudoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AddPremium):
		if b.addPremHandler != nil {
			b.addPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmPremium):
		if b.rmPremHandler != nil {
			b.rmPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Premium):
		if b.checkPremHandler != nil {
			b.checkPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Help):
		if b.helpHandler != nil {
			b.helpHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Ping):
		if b.pingHandler != nil {
			b.pingHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Stats):
		if b.statsHandler != nil {
			b.statsHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Log):
		if b.logHandler != nil {
			b.logHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Restart):
		if b.restartHandler != nil {
			b.restartHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Speedtest):
		if b.speedtestHandler != nil {
			b.speedtestHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.MediaInfo):
		if b.mediaInfoHandler != nil {
			b.mediaInfoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Broadcast):
		if b.broadcastHandler != nil {
			b.broadcastHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Imdb):
		if b.imdbHandler != nil {
			b.imdbHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Anime):
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
	case strings.HasPrefix(data, "stats") || strings.HasPrefix(data, "wzmlx"):
		if b.statsCbHandler != nil {
			b.statsCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "status"):
		if b.statusCbHandler != nil {
			b.statusCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data)
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

		<-ctx.Done()
		return ctx.Err()
	})
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
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("❌ Error downloading <b>token.pickle</b>: %v", err), m.ID, nil)
					return true
				}
				// Reload Google Drive OAuth credentials immediately
				gClient := downloader.GetGDriveClient()
				if gClient != nil {
					_ = gClient.LoadCredentials("token.pickle")
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✅ <b>token.pickle</b> received and saved! Google Drive OAuth credentials successfully reloaded.", m.ID, nil)
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
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("❌ Error downloading <b>config.env</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = config.LoadConfig("config.env")
				_, _ = b.sender.SendMessage(ctx, peer, "✅ <b>config.env</b> received and saved! Configurations reloaded.", m.ID, nil)
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
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("❌ Error downloading <b>cookies.txt</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✅ <b>cookies.txt</b> received and saved for yt-dlp & aria2c!", m.ID, nil)
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
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("❌ Error downloading <b>accounts.zip</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✅ <b>accounts.zip</b> received and saved successfully!", m.ID, nil)
				return true
			}

			// 1B. Custom Thumbnail document
			if strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") || strings.HasSuffix(lowerName, ".png") {
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
						_, _ = b.sender.SendMessage(ctx, peer, "✅ <b>Custom thumbnail saved!</b> It will be attached to all your future leeched files.", m.ID, nil)
						return true
					}
				}
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
					_, _ = b.sender.SendMessage(ctx, peer, "✅ <b>Custom thumbnail saved!</b> It will be attached to all your future leeched files.", m.ID, nil)
					return true
				}
			}
		}
	}

	return false
}
