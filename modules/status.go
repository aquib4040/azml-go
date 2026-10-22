package modules

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type StatusReplyInfo struct {
	ChatID      int64
	MessageID   int
	Peer        tg.InputPeerClass
	CurrentPage int
	LastEdit    time.Time
}

type StatusModule struct {
	sender        *telegram.MessageSender
	taskMgr       *ext.TaskManager
	statusReplies map[int64]*StatusReplyInfo
	chatPages     map[int64]int
	mu            sync.RWMutex
	stopTicker    chan struct{}
}

func NewStatusModule(sender *telegram.MessageSender) *StatusModule {
	mod := &StatusModule{
		sender:        sender,
		taskMgr:       ext.GetTaskManager(),
		statusReplies: make(map[int64]*StatusReplyInfo),
		chatPages:     make(map[int64]int),
		stopTicker:    make(chan struct{}),
	}
	mod.startPeriodicUpdateLoop()
	return mod
}

func (m *StatusModule) startPeriodicUpdateLoop() {
	go func() {
		cfg := config.Get()
		interval := cfg.StatusUpdateInterval
		if interval <= 0 {
			interval = 10
		}
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.UpdateAllMessages(context.Background(), false)
			case <-m.stopTicker:
				return
			}
		}
	}()
}

func (m *StatusModule) getChatPage(chatID int64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.chatPages[chatID]; ok && p > 0 {
		return p
	}
	return 1
}

func (m *StatusModule) HandleStatus(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	page := m.getChatPage(msg.ChatID)
	totalPages := m.taskMgr.GetTotalPages()
	if totalPages > 0 && page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}

	m.mu.Lock()
	oldInfo, hasOld := m.statusReplies[msg.ChatID]
	m.mu.Unlock()

	// Delete old status message if exists in this chat (matching Python AZML sendStatusMessage)
	if hasOld && oldInfo != nil && oldInfo.MessageID > 0 {
		_ = m.sender.DeleteMessage(ctx, oldInfo.Peer, []int{oldInfo.MessageID})
	}

	text, btn := m.taskMgr.BuildStatusMessage(page)
	var markup tg.ReplyMarkupClass
	if btn != nil {
		markup = btn.BuildMenu(3)
	}

	sentID, err := m.sender.SendMessage(ctx, peer, text, msg.MessageID, markup)
	if err != nil || sentID <= 0 {
		return
	}

	m.mu.Lock()
	m.statusReplies[msg.ChatID] = &StatusReplyInfo{
		ChatID:      msg.ChatID,
		MessageID:   sentID,
		Peer:        peer,
		CurrentPage: page,
		LastEdit:    time.Now(),
	}
	m.chatPages[msg.ChatID] = page
	m.mu.Unlock()
}

func (m *StatusModule) SetStatusMessage(chatID int64, msgID int, peer tg.InputPeerClass) {
	if msgID <= 0 {
		return
	}
	m.mu.Lock()
	m.statusReplies[chatID] = &StatusReplyInfo{
		ChatID:      chatID,
		MessageID:   msgID,
		Peer:        peer,
		CurrentPage: 1,
		LastEdit:    time.Time{},
	}
	m.mu.Unlock()
	go m.UpdateAllMessages(context.Background(), true)
}

func (m *StatusModule) RemoveStatusMessage(chatID int64) {
	m.mu.Lock()
	delete(m.statusReplies, chatID)
	m.mu.Unlock()
}

func (m *StatusModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string) {
	parts := strings.Fields(data)
	if len(parts) < 2 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}

	// Lookup chatID by msgID
	var chatID int64
	m.mu.RLock()
	for cID, info := range m.statusReplies {
		if info.MessageID == msgID {
			chatID = cID
			break
		}
	}
	m.mu.RUnlock()

	switch parts[1] {
	case "close":
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Status closed", false)
		m.mu.Lock()
		if chatID != 0 {
			delete(m.statusReplies, chatID)
		}
		m.mu.Unlock()
		return

	case "ref":
		page := 1
		if len(parts) >= 3 {
			page, _ = strconv.Atoi(parts[2])
		} else if chatID != 0 {
			page = m.getChatPage(chatID)
		}

		totalPages := m.taskMgr.GetTotalPages()
		if totalPages > 0 && page > totalPages {
			page = totalPages
		}
		if page < 1 {
			page = 1
		}

		// Update tracked page for this chat so subsequent edits stay on this page
		if chatID != 0 {
			m.mu.Lock()
			m.chatPages[chatID] = page
			if info, ok := m.statusReplies[chatID]; ok {
				info.CurrentPage = page
				info.LastEdit = time.Now()
			}
			m.mu.Unlock()
		}

		text, btn := m.taskMgr.BuildStatusMessage(page)
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(3)
		}
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "Status Refreshed!", false)

	case "page", "nex", "pre":
		page := 1
		totalPages := m.taskMgr.GetTotalPages()
		curr := m.getChatPage(chatID)

		if len(parts) >= 3 {
			page, _ = strconv.Atoi(parts[2])
		} else if parts[1] == "nex" {
			page = curr + 1
			if totalPages > 0 && page > totalPages {
				page = 1
			}
		} else if parts[1] == "pre" {
			page = curr - 1
			if page < 1 {
				page = totalPages
			}
		}

		if totalPages > 0 && page > totalPages {
			page = totalPages
		}
		if page < 1 {
			page = 1
		}

		// Update tracked page for this chat so subsequent edits stay on this page
		if chatID != 0 {
			m.mu.Lock()
			m.chatPages[chatID] = page
			if info, ok := m.statusReplies[chatID]; ok {
				info.CurrentPage = page
				info.LastEdit = time.Now()
			}
			m.mu.Unlock()
		}

		text, btn := m.taskMgr.BuildStatusMessage(page)
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(3)
		}
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
}

// UpdateAllMessages periodically refreshes active status messages on their current active page
func (m *StatusModule) UpdateAllMessages(ctx context.Context, force bool) {
	m.mu.Lock()
	if len(m.statusReplies) == 0 {
		m.mu.Unlock()
		return
	}
	replies := make([]*StatusReplyInfo, 0, len(m.statusReplies))
	for _, r := range m.statusReplies {
		replies = append(replies, r)
	}
	m.mu.Unlock()

	totalPages := m.taskMgr.GetTotalPages()

	for _, r := range replies {
		if r.MessageID == 0 {
			continue
		}
		if !force && time.Since(r.LastEdit) < 3*time.Second {
			continue
		}

		targetPage := r.CurrentPage
		if targetPage < 1 {
			targetPage = 1
		}
		if totalPages > 0 && targetPage > totalPages {
			targetPage = totalPages
			m.mu.Lock()
			r.CurrentPage = targetPage
			m.chatPages[r.ChatID] = targetPage
			m.mu.Unlock()
		}

		// Stays on targetPage (e.g. Page 2 stays on Page 2)
		text, btn := m.taskMgr.BuildStatusMessage(targetPage)
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(3)
		}

		err := m.sender.EditMessage(ctx, r.Peer, r.MessageID, text, markup)
		if err != nil {
			if strings.Contains(err.Error(), "MESSAGE_ID_INVALID") || strings.Contains(err.Error(), "400") {
				m.mu.Lock()
				delete(m.statusReplies, r.ChatID)
				m.mu.Unlock()
			}
		} else {
			m.mu.Lock()
			r.LastEdit = time.Now()
			m.mu.Unlock()
		}
	}
}


