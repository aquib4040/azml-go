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

func matchID(configuredID, actualID int64) bool {
	if configuredID == actualID {
		return true
	}
	if actualID > 0 {
		if configuredID == -actualID {
			return true
		}
		if configuredID == -(1000000000000 + actualID) {
			return true
		}
	} else if configuredID < 0 {
		if actualID == -configuredID {
			return true
		}
		if -(1000000000000 + actualID) == configuredID {
			return true
		}
	}
	return false
}

func isChatOrUserAuth(id int64, userCache *types.SafeUserMap) bool {
	if u := userCache.Get(id); u != nil && u.IsAuth {
		return true
	}
	if id > 0 {
		if u := userCache.Get(-(1000000000000 + id)); u != nil && u.IsAuth {
			return true
		}
		if u := userCache.Get(-id); u != nil && u.IsAuth {
			return true
		}
	} else if id < -1000000000000 {
		plainID := -(id + 1000000000000)
		if u := userCache.Get(plainID); u != nil && u.IsAuth {
			return true
		}
	}
	return false
}

func IsAuthorized(chatID, userID int64, userCache *types.SafeUserMap) bool {
	if IsOwner(userID) || IsSudo(userID, userCache) {
		return true
	}
	cfg := config.Get()
	for _, id := range cfg.AuthorizedChats {
		if matchID(id, chatID) || matchID(id, userID) {
			return true
		}
	}
	if isChatOrUserAuth(chatID, userCache) || isChatOrUserAuth(userID, userCache) {
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

