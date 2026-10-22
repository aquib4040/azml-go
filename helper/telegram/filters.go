package telegram

import (
	"time"

	"azml-go/types"
	"azml-go/config"
)

func IsOwner(userID int64) bool {
	cfg := config.Get()
	return userID == cfg.OwnerID
}

func IsSudo(userID int64, userCache *types.SafeUserMap) bool {
	if IsOwner(userID) {
		return true
	}
	cfg := config.Get()
	for _, id := range cfg.SudoUsers {
		if id == userID {
			return true
		}
	}
	if u := userCache.Get(userID); u != nil && u.IsSudo {
		return true
	}
	return false
}

func IsAuthorized(chatID, userID int64, userCache *types.SafeUserMap) bool {
	if IsOwner(userID) || IsSudo(userID, userCache) {
		return true
	}
	cfg := config.Get()
	for _, id := range cfg.AuthorizedChats {
		if id == chatID || id == userID {
			return true
		}
	}
	if u := userCache.Get(chatID); u != nil && u.IsAuth {
		return true
	}
	if u := userCache.Get(userID); u != nil && u.IsAuth {
		return true
	}
	return false
}

func IsBlacklisted(userID int64, userCache *types.SafeUserMap) bool {
	if IsOwner(userID) {
		return false
	}
	cfg := config.Get()
	for _, id := range cfg.BlacklistUsers {
		if id == userID {
			return true
		}
	}
	if u := userCache.Get(userID); u != nil && u.IsBlacklist {
		return true
	}
	return false
}

func IsPremium(userID int64, userCache *types.SafeUserMap) bool {
	if IsOwner(userID) || IsSudo(userID, userCache) {
		return true
	}
	if u := userCache.Get(userID); u != nil && u.IsPremium {
		if u.ExpiryTime == 0 || u.ExpiryTime > time.Now().Unix() {
			return true
		}
		u.IsPremium = false
	}
	return false
}

