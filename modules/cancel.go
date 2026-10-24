package modules

import (
	"context"
	"fmt"
	"strings"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type CancelModule struct {
	sender  *telegram.MessageSender
	taskMgr *ext.TaskManager
	userMap *types.SafeUserMap
}

func NewCancelModule(sender *telegram.MessageSender, userMap *types.SafeUserMap) *CancelModule {
	return &CancelModule{
		sender:  sender,
		taskMgr: ext.GetTaskManager(),
		userMap: userMap,
	}
}

func (m *CancelModule) HandleCancel(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	isCancel, extractedGID := telegram.MatchCancelCommand(msg.Text)
	if !isCancel && !msg.IsReply {
		return
	}

	if ext.IsPendingThumbSession(msg.UserID) {
		ext.ClearPendingThumbSession(msg.UserID)
		_, _ = m.sender.SendMessage(ctx, peer, "✅ <b>Custom thumbnail upload cancelled.</b>", msg.MessageID, nil)
		return
	}

	gid := extractedGID
	if gid == "" && msg.IsReply {
		for _, t := range m.taskMgr.GetAllTasks() {
			if t.MessageID() == msg.ReplyToID {
				gid = t.GID()
				break
			}
		}
		if gid == "" && msg.ReplyText != "" {
			for _, t := range m.taskMgr.GetAllTasks() {
				if strings.Contains(msg.ReplyText, t.GID()) {
					gid = t.GID()
					break
				}
			}
		}
	}

	if strings.ToLower(gid) == "all" {
		m.HandleCancelAll(ctx, peer, msg)
		return
	}

	if gid == "" {
		reply := "Reply to an active Command message which was used to start the download or send <code>/cancel_GID</code> to cancel it!"
		_, _ = m.sender.SendMessage(ctx, peer, reply, msg.MessageID, nil)
		return
	}

	task := m.taskMgr.GetTask(gid)
	if task == nil {
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("GID: <code>%s</code> Not Found.", gid), msg.MessageID, nil)
		return
	}

	// Permission: Owner, Sudo, or the user who started the task (same as Python AZML)
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) && task.UserID() != msg.UserID {
		_, _ = m.sender.SendMessage(ctx, peer, "This task is not for you!", msg.MessageID, nil)
		return
	}

	_ = task.Cancel()
	m.taskMgr.RemoveTask(gid)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ Task <code>%s</code> cancelled successfully!", gid), msg.MessageID, nil)
}

func (m *CancelModule) HandleCancelAll(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "This command is only for Sudo/Owner!", msg.MessageID, nil)
		return
	}

	tasks := m.taskMgr.GetAllTasks()
	if len(tasks) == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "No active tasks!", msg.MessageID, nil)
		return
	}

	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("📥 "+telegram.ToSmallCaps("Downloading"), "canall downloading", "primary")
	btn.IButtonStyled("📤 "+telegram.ToSmallCaps("Uploading"), "canall uploading", "primary")
	btn.IButtonStyled("⏸️ "+telegram.ToSmallCaps("Paused"), "canall paused", "primary")
	btn.IButtonStyled("⏳ "+telegram.ToSmallCaps("Queued Dl"), "canall queueddl", "primary")
	btn.IButtonStyled("⌛ "+telegram.ToSmallCaps("Queued Up"), "canall queueup", "primary")
	btn.IButtonStyled("⛔ "+telegram.ToSmallCaps("All Tasks"), "canall all", "danger")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "canall close", "danger")

	_, _ = m.sender.SendMessage(ctx, peer, "<b>Choose tasks to cancel:</b>", msg.MessageID, btn.BuildMenu(2))
}

func (m *CancelModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	if !telegram.IsOwner(fromUID) && !telegram.IsSudo(fromUID, m.userMap) {
		_ = m.sender.AnswerCallback(ctx, queryID, "Only Sudo/Owner can cancel tasks!", true)
		return
	}

	parts := strings.Fields(data)
	if len(parts) < 2 {
		return
	}

	action := strings.ToLower(parts[1])
	if action == "close" {
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
		return
	}

	count := m.taskMgr.CancelAll(action)
	_ = m.sender.EditMessage(ctx, peer, msgID, fmt.Sprintf("Cancelled <code>%d</code> task(s) matching '%s'", count, action), nil)
	_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Cancelled %d tasks", count), false)
}
