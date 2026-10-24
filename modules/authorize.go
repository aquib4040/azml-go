package modules

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

var timePattern = regexp.MustCompile(`(?i)(\d+)\s*([a-zA-Z]+)`)

type AuthorizeModule struct {
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewAuthorizeModule(sender *telegram.MessageSender, userMap *types.SafeUserMap) *AuthorizeModule {
	return &AuthorizeModule{
		sender:  sender,
		userMap: userMap,
	}
}

func (m *AuthorizeModule) HandleAuthorize(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can authorize!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		targetID = msg.ChatID
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsAuth = true
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Authorized:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleUnauthorize(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can unauthorize!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		targetID = msg.ChatID
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsAuth = false
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Unauthorized:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleAddSudo(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Bot Owner can add Sudo!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsSudo = true
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Promoted as Sudo:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleRmSudo(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Bot Owner can remove Sudo!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsSudo = false
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Demoted from Sudo:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleAddPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can add Premium!", msg.MessageID, nil)
		return
	}

	targetID, durationSec := parsePremiumArgs(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "<b>Usage:</b> <code>/addpremium [user_id] [duration]</code> (e.g. <code>/addpremium 12345 30d</code>)", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsPremium = true
	if durationSec > 0 {
		u.ExpiryTime = time.Now().Unix() + durationSec
	} else {
		u.ExpiryTime = 0 // Lifetime
	}
	_ = ext.DB.SaveUser(u)

	expiryStr := "Lifetime"
	if u.ExpiryTime > 0 {
		expiryStr = ext.GetReadableTime(durationSec)
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>User Promoted to Premium!</b>\n• <b>User ID:</b> <code>%d</code>\n• <b>Duration:</b> <code>%s</code>\n• <b>Benefits:</b> 4GB upload, No token verification", targetID, expiryStr), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleRmPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can remove Premium!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsPremium = false
	u.ExpiryTime = 0
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Premium Removed for:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleCheckPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	targetID := msg.UserID
	if (telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap)) && msg.IsReply {
		targetID = msg.ReplyUserID
	}

	if telegram.IsPremium(targetID, m.userMap) {
		u := m.userMap.Get(targetID)
		remStr := "Lifetime"
		if u != nil && u.ExpiryTime > 0 {
			remSec := u.ExpiryTime - time.Now().Unix()
			remStr = ext.GetReadableTime(remSec)
		}
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Premium User Status:</b>\n• <b>User ID:</b> <code>%d</code>\n✓ <b>Status:</b> Active\n• <b>Remaining:</b> <code>%s</code>", targetID, remStr), msg.MessageID, nil)
	} else {
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Premium User Status:</b>\n• <b>User ID:</b> <code>%d</code>\n× <b>Status:</b> Not Premium", targetID), msg.MessageID, nil)
	}
}

func extractTargetID(msg *types.MessageContext) int64 {
	tokens := strings.Fields(msg.Text)
	if len(tokens) > 1 {
		if id, err := strconv.ParseInt(tokens[1], 10, 64); err == nil {
			return id
		}
	}
	if msg.IsReply {
		return msg.ReplyUserID
	}
	return 0
}

func parsePremiumArgs(msg *types.MessageContext) (int64, int64) {
	tokens := strings.Fields(msg.Text)
	var targetID int64
	var totalSeconds int64

	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if id, err := strconv.ParseInt(t, 10, 64); err == nil && targetID == 0 {
			targetID = id
			continue
		}

		if m := timePattern.FindStringSubmatch(t); len(m) > 2 {
			val, _ := strconv.ParseInt(m[1], 10, 64)
			unit := strings.ToLower(m[2])
			switch unit {
			case "s", "sec", "second":
				totalSeconds += val
			case "m", "min", "minute":
				totalSeconds += val * 60
			case "h", "hr", "hour":
				totalSeconds += val * 3600
			case "d", "day", "days":
				totalSeconds += val * 86400
			case "w", "week":
				totalSeconds += val * 7 * 86400
			case "mo", "month":
				totalSeconds += val * 30 * 86400
			case "y", "yr", "year":
				totalSeconds += val * 365 * 86400
			}
		}
	}

	if targetID == 0 && msg.IsReply {
		targetID = msg.ReplyUserID
	}

	return targetID, totalSeconds
}
