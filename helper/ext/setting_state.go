package ext

import (
	"sync"
	"time"
)

type PendingSettingSession struct {
	Key         string
	ChatID      int64
	PromptMsgID int
	ExpiresAt   time.Time
}

type settingStateManager struct {
	mu      sync.RWMutex
	pending map[int64]*PendingSettingSession
}

var globalSettingState = &settingStateManager{
	pending: make(map[int64]*PendingSettingSession),
}

// SetPendingSettingSession sets a pending prompt listener for a user setting
func SetPendingSettingSession(userID int64, key string, chatID int64, promptMsgID int, d time.Duration) {
	globalSettingState.mu.Lock()
	defer globalSettingState.mu.Unlock()
	globalSettingState.pending[userID] = &PendingSettingSession{
		Key:         key,
		ChatID:      chatID,
		PromptMsgID: promptMsgID,
		ExpiresAt:   time.Now().Add(d),
	}
}

// GetPendingSettingSession returns the pending setting session if active
func GetPendingSettingSession(userID int64, chatID ...int64) (*PendingSettingSession, bool) {
	globalSettingState.mu.Lock()
	defer globalSettingState.mu.Unlock()
	sess, exists := globalSettingState.pending[userID]
	if !exists {
		return nil, false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(globalSettingState.pending, userID)
		return nil, false
	}
	if len(chatID) > 0 && chatID[0] != 0 && sess.ChatID != 0 {
		if sess.ChatID != chatID[0] {
			return nil, false
		}
	}
	return sess, true
}

// ClearPendingSettingSession removes the pending setting session
func ClearPendingSettingSession(userID int64) {
	globalSettingState.mu.Lock()
	defer globalSettingState.mu.Unlock()
	delete(globalSettingState.pending, userID)
}

type PendingBotSettingSession struct {
	Key         string
	ChatID      int64
	PromptMsgID int
	Page        int
	ExpiresAt   time.Time
}

type botSettingStateManager struct {
	mu      sync.RWMutex
	pending map[int64]*PendingBotSettingSession
}

var globalBotSettingState = &botSettingStateManager{
	pending: make(map[int64]*PendingBotSettingSession),
}

// SetPendingBotSettingSession sets a pending prompt listener for a bot setting edit
func SetPendingBotSettingSession(userID int64, key string, chatID int64, promptMsgID int, page int, d time.Duration) {
	globalBotSettingState.mu.Lock()
	defer globalBotSettingState.mu.Unlock()
	globalBotSettingState.pending[userID] = &PendingBotSettingSession{
		Key:         key,
		ChatID:      chatID,
		PromptMsgID: promptMsgID,
		Page:        page,
		ExpiresAt:   time.Now().Add(d),
	}
}

// GetPendingBotSettingSession returns the pending bot setting session if active
func GetPendingBotSettingSession(userID int64, chatID ...int64) (*PendingBotSettingSession, bool) {
	globalBotSettingState.mu.Lock()
	defer globalBotSettingState.mu.Unlock()
	sess, exists := globalBotSettingState.pending[userID]
	if !exists {
		return nil, false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(globalBotSettingState.pending, userID)
		return nil, false
	}
	if len(chatID) > 0 && chatID[0] != 0 && sess.ChatID != 0 {
		if sess.ChatID != chatID[0] {
			return nil, false
		}
	}
	return sess, true
}

// ClearPendingBotSettingSession removes the pending bot setting session
func ClearPendingBotSettingSession(userID int64) {
	globalBotSettingState.mu.Lock()
	defer globalBotSettingState.mu.Unlock()
	delete(globalBotSettingState.pending, userID)
}
