package modules

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestStatusModuleTracking(t *testing.T) {
	mod := NewStatusModule(nil)

	chatID := int64(-1001234567890)
	peer := &tg.InputPeerChannel{
		ChannelID:  1234567890,
		AccessHash: 987654321,
	}

	// Initially, no status message
	if mod.HasStatusMessage(chatID) {
		t.Errorf("expected no status message initially")
	}
	if mod.GetStatusMessageID(chatID) != 0 {
		t.Errorf("expected statusMsgID 0 initially")
	}

	// Set status message
	mod.SetStatusMessage(chatID, 42, peer)

	if !mod.HasStatusMessage(chatID) {
		t.Errorf("expected status message to exist after SetStatusMessage")
	}
	if id := mod.GetStatusMessageID(chatID); id != 42 {
		t.Errorf("expected statusMsgID 42, got %d", id)
	}

	// Check stored peer
	mod.mu.RLock()
	storedPeer := mod.chatPeers[chatID]
	mod.mu.RUnlock()
	if storedPeer == nil {
		t.Errorf("expected stored peer for chatID %d", chatID)
	}

	// Remove status message
	mod.RemoveStatusMessage(chatID)
	if mod.HasStatusMessage(chatID) {
		t.Errorf("expected no status message after RemoveStatusMessage")
	}
}
