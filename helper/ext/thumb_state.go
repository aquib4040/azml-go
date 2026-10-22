package ext

import (
	"sync"
	"time"
)

type PendingThumbSession struct {
	ChatID      int64
	PromptMsgID int
	ExpiresAt   time.Time
}

type thumbStateManager struct {
	mu      sync.RWMutex
	pending map[int64]*PendingThumbSession
}

var globalThumbState = &thumbStateManager{
	pending: make(map[int64]*PendingThumbSession),
}

// SetPendingThumbSession registers that userID is expected to upload a custom thumbnail within the given duration
func SetPendingThumbSession(userID int64, chatID int64, promptMsgID int, d time.Duration) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	globalThumbState.pending[userID] = &PendingThumbSession{
		ChatID:      chatID,
		PromptMsgID: promptMsgID,
		ExpiresAt:   time.Now().Add(d),
	}
}

// IsPendingThumbSession checks if userID has an active pending thumbnail upload session
func IsPendingThumbSession(userID int64) bool {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	sess, exists := globalThumbState.pending[userID]
	if !exists {
		return false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(globalThumbState.pending, userID)
		return false
	}
	return true
}

// GetPendingThumbSession returns the active session for userID, if any
func GetPendingThumbSession(userID int64) (*PendingThumbSession, bool) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	sess, exists := globalThumbState.pending[userID]
	if !exists {
		return nil, false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(globalThumbState.pending, userID)
		return nil, false
	}
	return sess, true
}

// ClearPendingThumbSession removes any pending session for userID
func ClearPendingThumbSession(userID int64) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	delete(globalThumbState.pending, userID)
}
