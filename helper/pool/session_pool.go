package pool

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

const (
	AuthTimeout = 30 * time.Second // Generous timeout for MTProto DH key exchange
)

func isFatalTokenError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") ||
		strings.Contains(s, "AUTH_KEY_UNREGISTERED") ||
		strings.Contains(s, "TOKEN_INVALID") ||
		strings.Contains(s, "USER_DEACTIVATED") ||
		strings.Contains(s, "BOT_METHOD_INVALID")
}

// CachedDocument stores resolved document metadata and file location per bot session.
type CachedDocument struct {
	Size       int64
	FileName   string
	Location   tg.InputFileLocationClass
	ResolvedAt time.Time
	Duration   float64
	Width      int
	Height     int
}

// BotSession represents a single authenticated gotd MTProto bot session.
type BotSession struct {
	Index           int
	Token           string
	BotID           int64
	Username        string
	client          *telegram.Client
	API             *tg.Client
	ctx             context.Context
	cancel          context.CancelFunc
	ready           chan struct{}
	readyErr        error
	lastUsed        atomic.Int64
	cooling         atomic.Int64
	InFlightChunks  atomic.Int32
	ActiveDownloads atomic.Int32
	ActiveUploads   atomic.Int32
	TotalDownloads  atomic.Int64
	accessCache     sync.Map   // int64 (raw channel ID) → int64 (access hash)
	canPostCache    sync.Map   // int64 (raw channel ID) → bool
	docCache        sync.Map   // string ("chatID_msgID") → *CachedDocument
	chunkMu         sync.Mutex // Serializes chunk downloads on this bot session
	lastReqTime     time.Time  // Anti-Flood pacing timestamp (protected by chunkMu)
	pool            *SessionPool
}

func (bot *BotSession) SafeSuffix() string {
	s := bot.Token
	if len(s) > 6 {
		return s[len(s)-6:]
	}
	return s
}

func (bot *BotSession) LockChunk() {
	bot.chunkMu.Lock()
	// Anti-FloodWait Pacing: Telegram DC throttles if MTProto requests arrive faster than ~120ms on a single session.
	if !bot.lastReqTime.IsZero() {
		if elapsed := time.Since(bot.lastReqTime); elapsed < 120*time.Millisecond {
			time.Sleep(120*time.Millisecond - elapsed)
		}
	}
}

func (bot *BotSession) UnlockChunk() {
	bot.lastReqTime = time.Now()
	bot.chunkMu.Unlock()
}

func (bot *BotSession) StartDownload() {
	bot.ActiveDownloads.Add(1)
	bot.TotalDownloads.Add(1)
	bot.Touch()
}

func (bot *BotSession) EndDownload() {
	if count := bot.ActiveDownloads.Add(-1); count < 0 {
		bot.ActiveDownloads.Store(0)
	}
}

func (bot *BotSession) AcquireChunkLoad() {
	bot.InFlightChunks.Add(1)
	bot.Touch()
}

func (bot *BotSession) ReleaseChunkLoad() {
	if count := bot.InFlightChunks.Add(-1); count < 0 {
		bot.InFlightChunks.Store(0)
	}
}

func (bot *BotSession) Touch() {
	bot.lastUsed.Store(time.Now().Unix())
}

func (bot *BotSession) SetCooldown(seconds int64) {
	if seconds <= 0 {
		seconds = 1
	}
	bot.cooling.Store(time.Now().Unix() + seconds)
}

func (bot *BotSession) IsCooling() bool {
	return time.Now().Unix() < bot.cooling.Load()
}

func (bot *BotSession) RemainingCooldown() time.Duration {
	rem := bot.cooling.Load() - time.Now().Unix()
	if rem <= 0 {
		return 0
	}
	return time.Duration(rem) * time.Second
}

// SessionPool manages MTProto bot sessions with:
//   - Non-blocking parallel startup authentication
//   - Fast failure detection with in-memory invalid token blacklisting
//   - Round-robin load balancing across worker bots
type SessionPool struct {
	mu            sync.RWMutex
	sessions      []*BotSession
	tokenMap      map[string]*BotSession
	invalidTokens sync.Map // token (string) -> true
	baseDataPath  string
	dumpChatID    int64
	rrIndex       int
}

func (p *SessionPool) SetDumpChatID(id int64) {
	p.mu.Lock()
	p.dumpChatID = id
	p.mu.Unlock()
}

func NewSessionPool(baseDataPath string) *SessionPool {
	return &SessionPool{
		sessions:     make([]*BotSession, 0),
		tokenMap:     make(map[string]*BotSession),
		baseDataPath: baseDataPath,
	}
}

// FormatChannelID ensures channel ID starts with -100
func FormatChannelID(id int64) int64 {
	if id > 0 {
		s := fmt.Sprintf("%d", id)
		if !strings.HasPrefix(s, "100") {
			s = "100" + s
		}
		if parsed, err := strconv.ParseInt("-"+s, 10, 64); err == nil {
			return parsed
		}
	}
	return id
}

// RawChannelID extracts the raw Telegram channel ID from -100XXXXXXXXX format
func RawChannelID(chatID int64) int64 {
	s := strconv.FormatInt(chatID, 10)
	s = strings.TrimPrefix(s, "-")
	if strings.HasPrefix(s, "100") && len(s) > 3 {
		if raw, err := strconv.ParseInt(s[3:], 10, 64); err == nil {
			return raw
		}
	}
	if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
		return parsed
	}
	return chatID
}

// IsTokenInvalid checks whether a bot token has been blacklisted in memory
func (p *SessionPool) IsTokenInvalid(token string) bool {
	_, bad := p.invalidTokens.Load(token)
	return bad
}

// MarkTokenInvalid flags a token as invalid in memory so it is skipped immediately
func (p *SessionPool) MarkTokenInvalid(token string, reason string) {
	safeSuffix := token
	if len(safeSuffix) > 6 {
		safeSuffix = safeSuffix[len(safeSuffix)-6:]
	}
	p.invalidTokens.Store(token, true)
	log.Printf("[SessionPool] Token ...%s flagged as INVALID in-memory: %s (will be skipped)", safeSuffix, reason)

	p.mu.Lock()
	defer p.mu.Unlock()
	if bot, exists := p.tokenMap[token]; exists {
		bot.cancel()
		delete(p.tokenMap, token)
		var kept []*BotSession
		for _, s := range p.sessions {
			if s.Token != token {
				kept = append(kept, s)
			}
		}
		p.sessions = kept
	}
}

// InitSession authenticates a bot session. If authentication fails or times out,
// it marks the token as invalid in memory and skips it.
func (p *SessionPool) InitSession(apiID int, apiHash string, token string) (*BotSession, error) {
	if p.IsTokenInvalid(token) {
		return nil, fmt.Errorf("token is blacklisted as invalid in memory")
	}

	p.mu.Lock()
	if bot, exists := p.tokenMap[token]; exists {
		p.mu.Unlock()
		bot.lastUsed.Store(time.Now().Unix())
		select {
		case <-bot.ready:
			if bot.readyErr != nil {
				return nil, bot.readyErr
			}
			return bot, nil
		case <-time.After(AuthTimeout):
			return nil, fmt.Errorf("timeout waiting for session authentication")
		}
	}

	hash := md5.Sum([]byte(token))
	sessionDir := filepath.Join(p.baseDataPath, "session_"+hex.EncodeToString(hash[:]))
	_ = os.MkdirAll(sessionDir, 0755)

	safeSuffix := token
	if len(safeSuffix) > 6 {
		safeSuffix = safeSuffix[len(safeSuffix)-6:]
	}

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{
			Path: filepath.Join(sessionDir, "session.json"),
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	bot := &BotSession{
		Index:  len(p.sessions),
		Token:  token,
		client: client,
		ctx:    ctx,
		cancel: cancel,
		ready:  make(chan struct{}),
		pool:   p,
	}
	bot.lastUsed.Store(time.Now().Unix())

	p.sessions = append(p.sessions, bot)
	p.tokenMap[token] = bot
	p.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			err := client.Run(ctx, func(runCtx context.Context) error {
				status, err := client.Auth().Status(runCtx)
				if err != nil {
					bot.readyErr = fmt.Errorf("auth status check failed: %w", err)
					select {
					case <-bot.ready:
					default:
						close(bot.ready)
					}
					return bot.readyErr
				}

				if !status.Authorized {
					if _, err := client.Auth().Bot(runCtx, token); err != nil {
						bot.readyErr = fmt.Errorf("bot auth failed: %w", err)
						select {
						case <-bot.ready:
						default:
							close(bot.ready)
						}
						return bot.readyErr
					}
				}

				bot.API = client.API()

				botUsername := ""
				if self, err := client.Self(runCtx); err == nil && self != nil {
					bot.BotID = self.ID
					bot.Username = self.Username
					if self.Username != "" {
						botUsername = "@" + self.Username
					} else {
						botUsername = self.FirstName
					}
					log.Printf("[SessionPool] Worker bot authenticated: %s (ID: %d, token ...%s)", botUsername, self.ID, safeSuffix)
				} else {
					log.Printf("[SessionPool] Worker bot authenticated: token ...%s", safeSuffix)
				}

				p.mu.RLock()
				dumpID := p.dumpChatID
				p.mu.RUnlock()

				if dumpID != 0 {
					checkCtx, checkCancel := context.WithTimeout(runCtx, 10*time.Second)
					if inChan, err := bot.ResolveChannel(checkCtx, dumpID); err == nil && inChan != nil {
						log.Printf("[SessionPool] ✓ %s verified ADMIN access to Dump Channel (%d)", botUsername, dumpID)
					} else {
						log.Printf("[SessionPool] ⚠️ Warning: %s cannot access Dump Channel %d (%v). Ensure bot is added as ADMIN in channel!", botUsername, dumpID, err)
					}
					checkCancel()
				}

				select {
				case <-bot.ready:
				default:
					close(bot.ready)
				}

				<-runCtx.Done()
				return runCtx.Err()
			})

			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				// If authorization is revoked / token is invalid, mark immediately and do not loop
				if isFatalTokenError(err) {
					p.MarkTokenInvalid(token, err.Error())
					return
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			} else {
				return
			}
		}
	}()

	select {
	case <-bot.ready:
		if bot.readyErr != nil {
			if isFatalTokenError(bot.readyErr) {
				p.MarkTokenInvalid(token, bot.readyErr.Error())
			}
			return nil, bot.readyErr
		}
		return bot, nil
	case <-time.After(AuthTimeout):
		// Do NOT mark token as invalid on timeout! MTProto handshake will complete in background.
		log.Printf("[SessionPool] Notice: Token ...%s handshake still in progress (exceeded %v), continuing in background", safeSuffix, AuthTimeout)
		return nil, fmt.Errorf("timeout waiting for bot auth (token ...%s)", safeSuffix)
	}
}

func (bot *BotSession) ResolveChannel(ctx context.Context, chatID int64) (*tg.InputChannel, error) {
	rawID := RawChannelID(chatID)

	if hash, ok := bot.accessCache.Load(rawID); ok {
		return &tg.InputChannel{
			ChannelID:  rawID,
			AccessHash: hash.(int64),
		}, nil
	}

	if res, err := bot.API.MessagesGetChats(ctx, []int64{rawID}); err == nil && res != nil {
		var chats []tg.ChatClass
		switch c := res.(type) {
		case *tg.MessagesChats:
			chats = c.Chats
		case *tg.MessagesChatsSlice:
			chats = c.Chats
		}
		for _, chat := range chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == rawID && !ch.Min && ch.AccessHash != 0 {
				bot.accessCache.Store(rawID, ch.AccessHash)
				return &tg.InputChannel{
					ChannelID:  rawID,
					AccessHash: ch.AccessHash,
				}, nil
			}
		}
	}

	if res, err := bot.API.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{
			ChannelID:  rawID,
			AccessHash: 0,
		},
	}); err == nil && res != nil {
		var chats []tg.ChatClass
		switch c := res.(type) {
		case *tg.MessagesChats:
			chats = c.Chats
		case *tg.MessagesChatsSlice:
			chats = c.Chats
		}
		for _, chat := range chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == rawID && ch.AccessHash != 0 {
				bot.accessCache.Store(rawID, ch.AccessHash)
				return &tg.InputChannel{
					ChannelID:  rawID,
					AccessHash: ch.AccessHash,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("could not resolve channel %d access_hash", chatID)
}

func (bot *BotSession) ReleaseUpload() {
	if bot != nil {
		bot.ActiveUploads.Add(-1)
	}
}

// CanPostInChannel checks if this bot has posting permissions in the given channel.
func (bot *BotSession) CanPostInChannel(ctx context.Context, chatID int64) bool {
	rawID := RawChannelID(chatID)
	if val, ok := bot.canPostCache.Load(rawID); ok {
		return val.(bool)
	}

	inputChan, err := bot.ResolveChannel(ctx, chatID)
	if err != nil || inputChan == nil {
		bot.canPostCache.Store(rawID, false)
		return false
	}

	res, err := bot.API.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChan})
	if err != nil || res == nil {
		bot.canPostCache.Store(rawID, false)
		return false
	}

	var chats []tg.ChatClass
	switch c := res.(type) {
	case *tg.MessagesChats:
		chats = c.Chats
	case *tg.MessagesChatsSlice:
		chats = c.Chats
	}

	canPost := false
	for _, chat := range chats {
		if ch, ok := chat.(*tg.Channel); ok && ch.ID == rawID {
			if ch.Creator || ch.AdminRights.PostMessages {
				canPost = true
				break
			}
		}
	}

	bot.canPostCache.Store(rawID, canPost)
	return canPost
}

// ResolveDocument resolves channel message document and caches it for this specific bot session
func (bot *BotSession) ResolveDocument(
	ctx context.Context,
	chatID int64,
	messageID int64,
	forceRefresh bool,
) (int64, string, tg.InputFileLocationClass, error) {
	cacheKey := fmt.Sprintf("%d_%d", chatID, messageID)

	if !forceRefresh {
		if val, ok := bot.docCache.Load(cacheKey); ok {
			if cached, ok := val.(*CachedDocument); ok && time.Since(cached.ResolvedAt) < 30*time.Minute {
				return cached.Size, cached.FileName, cached.Location, nil
			}
		}
	}

	inputChannel, err := bot.ResolveChannel(ctx, chatID)
	if err != nil {
		return 0, "", nil, fmt.Errorf("failed to resolve channel %d for bot ...%s: %w", chatID, bot.SafeSuffix(), err)
	}

	res, err := bot.API.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: inputChannel,
		ID: []tg.InputMessageClass{
			&tg.InputMessageID{ID: int(messageID)},
		},
	})
	if err != nil {
		return 0, "", nil, fmt.Errorf("ChannelsGetMessages failed for bot ...%s: %w", bot.SafeSuffix(), err)
	}

	var location tg.InputFileLocationClass
	var totalSize int64
	var fileName string
	var duration float64
	var width int
	var height int

	var msgs []*tg.Message
	switch m := res.(type) {
	case *tg.MessagesChannelMessages:
		for _, msgClass := range m.Messages {
			if msg, ok := msgClass.(*tg.Message); ok {
				msgs = append(msgs, msg)
			}
		}
	case *tg.MessagesMessagesSlice:
		for _, msgClass := range m.Messages {
			if msg, ok := msgClass.(*tg.Message); ok {
				msgs = append(msgs, msg)
			}
		}
	case *tg.MessagesMessages:
		for _, msgClass := range m.Messages {
			if msg, ok := msgClass.(*tg.Message); ok {
				msgs = append(msgs, msg)
			}
		}
	}

	for _, msg := range msgs {
		if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
			if doc, ok := media.Document.(*tg.Document); ok {
				totalSize = doc.Size
				location = &tg.InputDocumentFileLocation{
					ID:            doc.ID,
					AccessHash:    doc.AccessHash,
					FileReference: doc.FileReference,
				}
				for _, attr := range doc.Attributes {
					switch a := attr.(type) {
					case *tg.DocumentAttributeFilename:
						fileName = a.FileName
					case *tg.DocumentAttributeVideo:
						duration = a.Duration
						width = a.W
						height = a.H
					case *tg.DocumentAttributeAudio:
						if duration == 0 {
							duration = float64(a.Duration)
						}
					}
				}
				break
			}
		}
	}

	if location == nil {
		return 0, "", nil, fmt.Errorf("document location not found for msg %d in channel %d (bot ...%s)", messageID, chatID, bot.SafeSuffix())
	}

	bot.docCache.Store(cacheKey, &CachedDocument{
		Size:       totalSize,
		FileName:   fileName,
		Location:   location,
		ResolvedAt: time.Now(),
		Duration:   duration,
		Width:      width,
		Height:     height,
	})

	return totalSize, fileName, location, nil
}

// GetCachedDoc retrieves cached document metadata if previously resolved by this bot session.
func (bot *BotSession) GetCachedDoc(chatID, messageID int64) *CachedDocument {
	cacheKey := fmt.Sprintf("%d_%d", chatID, messageID)
	if val, ok := bot.docCache.Load(cacheKey); ok {
		if cached, ok := val.(*CachedDocument); ok {
			return cached
		}
	}
	return nil
}

// GetDocumentDuration searches across all bot session caches for document duration in seconds.
func (p *SessionPool) GetDocumentDuration(chatID, messageID int64) float64 {
	cacheKey := fmt.Sprintf("%d_%d", chatID, messageID)
	p.mu.RLock()
	sessions := make([]*BotSession, len(p.sessions))
	copy(sessions, p.sessions)
	p.mu.RUnlock()

	for _, bot := range sessions {
		if val, ok := bot.docCache.Load(cacheKey); ok {
			if cached, ok := val.(*CachedDocument); ok && cached.Duration > 0 {
				return cached.Duration
			}
		}
	}
	return 0
}

// AcquireLeastLoadedBot selects the bot with minimum in-flight chunk load across all concurrent streams,
// filtering out cooling bots and respecting Telegram FloodWait cooldown intervals.
func (p *SessionPool) AcquireLeastLoadedBot(
	tokens []string,
	apiID int,
	apiHash string,
	excludeTokens map[string]bool,
) (*BotSession, error) {
	var validTokens []string
	for _, t := range tokens {
		if !p.IsTokenInvalid(t) {
			validTokens = append(validTokens, t)
		}
	}

	p.mu.Lock()
	var candidates []*BotSession
	if len(validTokens) > 0 {
		for _, token := range validTokens {
			if bot, exists := p.tokenMap[token]; exists {
				select {
				case <-bot.ready:
					if bot.readyErr == nil {
						candidates = append(candidates, bot)
					}
				default:
				}
			}
		}
	} else {
		for _, bot := range p.sessions {
			if !p.IsTokenInvalid(bot.Token) {
				select {
				case <-bot.ready:
					if bot.readyErr == nil {
						candidates = append(candidates, bot)
					}
				default:
				}
			}
		}
	}

	if len(candidates) == 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("no ready bot sessions in pool")
	}

	now := time.Now().Unix()

	// 1. Filter: non-excluded and NOT cooling down
	var available []*BotSession
	for _, b := range candidates {
		if excludeTokens != nil && excludeTokens[b.Token] {
			continue
		}
		if now >= b.cooling.Load() {
			available = append(available, b)
		}
	}

	// 2. If all non-excluded bots are cooling, try non-cooling even if previously in excludeTokens
	if len(available) == 0 {
		for _, b := range candidates {
			if now >= b.cooling.Load() {
				available = append(available, b)
			}
		}
	}

	// 3. If ALL bots are cooling down, calculate shortest remaining time and return error
	// CRITICAL: NEVER return a cooling bot into active streaming!
	if len(available) == 0 {
		var minRem int64 = 2
		first := true
		for _, b := range candidates {
			rem := b.cooling.Load() - now
			if rem > 0 && (first || rem < minRem) {
				minRem = rem
				first = false
			}
		}
		p.mu.Unlock()
		return nil, fmt.Errorf("all bots are cooling down (%ds remaining)", minRem)
	}

	// 4. Prioritize completely IDLE bots (InFlightChunks == 0) for zero-collision MTProto streaming
	var idleBots []*BotSession
	for _, b := range available {
		if b.InFlightChunks.Load() == 0 {
			idleBots = append(idleBots, b)
		}
	}

	var targetList []*BotSession
	if len(idleBots) > 0 {
		targetList = idleBots
	} else {
		// If all available bots are busy, pick from the least loaded
		minLoad := available[0].InFlightChunks.Load()
		for _, b := range available[1:] {
			if l := b.InFlightChunks.Load(); l < minLoad {
				minLoad = l
			}
		}
		for _, b := range available {
			if b.InFlightChunks.Load() == minLoad {
				targetList = append(targetList, b)
			}
		}
	}

	// 5. Round-robin select among targetList and atomically increment load under lock
	p.rrIndex = (p.rrIndex + 1) % len(targetList)
	chosen := targetList[p.rrIndex]
	chosen.AcquireChunkLoad()
	p.mu.Unlock()

	return chosen, nil
}

// ReleaseBotLoad decrements the in-flight chunk count for a bot
func (p *SessionPool) ReleaseBotLoad(bot *BotSession) {
	if bot != nil {
		bot.ReleaseChunkLoad()
	}
}

// GetNextAvailable picks a ready session from the provided tokens or pool round-robin,
// skipping any invalid or blacklisted tokens.
func (p *SessionPool) GetNextAvailable(tokens []string, apiID int, apiHash string) (*BotSession, error) {
	// Filter out invalid tokens
	var validTokens []string
	for _, t := range tokens {
		if !p.IsTokenInvalid(t) {
			validTokens = append(validTokens, t)
		}
	}

	p.mu.Lock()
	now := time.Now().Unix()

	var candidates []*BotSession
	if len(validTokens) > 0 {
		for _, token := range validTokens {
			if bot, exists := p.tokenMap[token]; exists {
				select {
				case <-bot.ready:
					if bot.readyErr == nil {
						candidates = append(candidates, bot)
					}
				default:
				}
			}
		}
	} else {
		for _, bot := range p.sessions {
			if !p.IsTokenInvalid(bot.Token) {
				select {
				case <-bot.ready:
					if bot.readyErr == nil {
						candidates = append(candidates, bot)
					}
				default:
				}
			}
		}
	}

	if len(candidates) > 0 {
		p.rrIndex = (p.rrIndex + 1) % len(candidates)
		startIdx := p.rrIndex
		p.mu.Unlock()

		for i := 0; i < len(candidates); i++ {
			idx := (startIdx + i) % len(candidates)
			bot := candidates[idx]
			if now >= bot.cooling.Load() && bot.ActiveDownloads.Load() < 16 {
				bot.lastUsed.Store(now)
				return bot, nil
			}
		}

		// Fallback: prefer bot that is not cooling down
		best := candidates[0]
		for _, b := range candidates[1:] {
			if now >= b.cooling.Load() && now < best.cooling.Load() {
				best = b
			} else if b.ActiveDownloads.Load() < best.ActiveDownloads.Load() {
				best = b
			}
		}
		best.lastUsed.Store(now)
		return best, nil
	}

	// If no candidate is currently ready, wait up to 5s on any session currently authenticating
	for _, bot := range p.sessions {
		if !p.IsTokenInvalid(bot.Token) {
			p.mu.Unlock()
			select {
			case <-bot.ready:
				if bot.readyErr == nil {
					bot.lastUsed.Store(time.Now().Unix())
					return bot, nil
				}
			case <-time.After(5 * time.Second):
			}
			p.mu.Lock()
		}
	}

	// If no sessions exist yet, attempt on-demand initialization of the first valid token
	if len(validTokens) > 0 {
		p.mu.Unlock()
		bot, err := p.InitSession(apiID, apiHash, validTokens[0])
		if err == nil && bot != nil {
			return bot, nil
		}
		p.mu.Lock()
	}

	p.mu.Unlock()
	return nil, fmt.Errorf("no ready gotd bot sessions available in pool")
}

// AcquireUploadBot picks a ready worker bot that has verified "Post Messages"
// admin permissions in the specified dump channel, prioritizing the bot with the lowest active uploads.
func (p *SessionPool) AcquireUploadBot(ctx context.Context, tokens []string, apiID int, apiHash string, dumpChatID int64) *BotSession {
	if dumpChatID == 0 {
		return nil
	}

	p.mu.Lock()
	var candidates []*BotSession
	for _, bot := range p.sessions {
		if !p.IsTokenInvalid(bot.Token) {
			select {
			case <-bot.ready:
				if bot.readyErr == nil && !bot.IsCooling() {
					candidates = append(candidates, bot)
				}
			default:
			}
		}
	}
	p.mu.Unlock()

	if len(candidates) == 0 {
		return nil
	}

	var bestBot *BotSession
	var minUploads int32 = 999999

	for _, bot := range candidates {
		if bot.CanPostInChannel(ctx, dumpChatID) {
			current := bot.ActiveUploads.Load()
			if current < minUploads {
				minUploads = current
				bestBot = bot
			}
		}
	}

	if bestBot != nil {
		bestBot.ActiveUploads.Add(1)
		return bestBot
	}

	return nil
}

// WarmupTokens starts background authentication of multi-worker tokens with controlled concurrency
func (p *SessionPool) WarmupTokens(apiID int, apiHash string, tokens []string, dumpChatID ...int64) {
	if len(tokens) == 0 {
		return
	}
	if len(dumpChatID) > 0 && dumpChatID[0] != 0 {
		p.SetDumpChatID(dumpChatID[0])
	}
	log.Printf("[Pool] Starting background warmup for %d worker bot tokens...", len(tokens))

	go func() {
		// Limit to 3 concurrent handshakes to prevent DC connection throttling
		sem := make(chan struct{}, 3)
		var wg sync.WaitGroup

		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if tok == "" || p.IsTokenInvalid(tok) {
				continue
			}

			wg.Add(1)
			go func(t string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				// Slight stagger between connection handshakes
				time.Sleep(250 * time.Millisecond)

				_, _ = p.InitSession(apiID, apiHash, t)
			}(tok)
		}

		wg.Wait()
		log.Printf("[Pool] Multi-token startup complete. %d active sessions in rotation.", p.ActiveSessionCount())
	}()
}

// ActiveSessionCount returns the count of currently connected, authenticated sessions
func (p *SessionPool) ActiveSessionCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, s := range p.sessions {
		if !p.IsTokenInvalid(s.Token) {
			select {
			case <-s.ready:
				if s.readyErr == nil {
					count++
				}
			default:
			}
		}
	}
	return count
}

func (p *SessionPool) StopAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, bot := range p.sessions {
		bot.cancel()
	}
	p.sessions = nil
	p.tokenMap = make(map[string]*BotSession)
}

// SessionWorkload captures realtime per-bot worker streaming load
type SessionWorkload struct {
	Index          int
	TokenSuffix    string
	ActiveStreams  int32
	InFlightChunks int32
	TotalStreams   int64
	IsReady        bool
	IsInvalid      bool
	IsCooling      bool
}

// GetWorkloads returns current stream connections and lifetime stats per bot token
func (p *SessionPool) GetWorkloads() []SessionWorkload {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var out []SessionWorkload
	for _, bot := range p.sessions {
		safeSuffix := bot.SafeSuffix()
		isReady := false
		select {
		case <-bot.ready:
			isReady = bot.readyErr == nil
		default:
		}

		out = append(out, SessionWorkload{
			Index:          bot.Index,
			TokenSuffix:    safeSuffix,
			ActiveStreams:  bot.ActiveDownloads.Load(),
			InFlightChunks: bot.InFlightChunks.Load(),
			TotalStreams:   bot.TotalDownloads.Load(),
			IsReady:        isReady,
			IsInvalid:      p.IsTokenInvalid(bot.Token),
			IsCooling:      bot.IsCooling(),
		})
	}
	return out
}
