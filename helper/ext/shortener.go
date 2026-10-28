package ext

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/google/uuid"
)

var httpClient = &http.Client{
	Timeout: 4 * time.Second,
}

type ShortenerEntry struct {
	Domain string
	APIKey string
}

var (
	shortenersList   []ShortenerEntry
	shortenersLock   sync.RWMutex
	shortenersLoaded bool
)

// LoadShorteners parses shorteners.txt if it exists
func LoadShorteners() {
	shortenersLock.Lock()
	defer shortenersLock.Unlock()
	shortenersList = nil
	shortenersLoaded = true

	data, err := os.ReadFile("shorteners.txt")
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		parts := strings.Fields(strings.TrimSpace(l))
		if len(parts) >= 2 {
			shortenersList = append(shortenersList, ShortenerEntry{
				Domain: parts[0],
				APIKey: parts[1],
			})
		}
	}
}

// GetShorteners returns a snapshot of active shorteners from shorteners.txt
func GetShorteners() []ShortenerEntry {
	shortenersLock.Lock()
	if !shortenersLoaded {
		shortenersLock.Unlock()
		LoadShorteners()
		shortenersLock.RLock()
		defer shortenersLock.RUnlock()
	} else {
		shortenersLock.Unlock()
		shortenersLock.RLock()
		defer shortenersLock.RUnlock()
	}
	res := make([]ShortenerEntry, len(shortenersList))
	copy(res, shortenersList)
	return res
}

func ShortURL(longURL string) string {
	cfg := config.Get()
	if !cfg.ShortenerEnabled {
		return longURL
	}

	shorteners := GetShorteners()
	var domain, apiKey string
	if len(shorteners) > 0 {
		idx := rand.Intn(len(shorteners))
		domain = shorteners[idx].Domain
		apiKey = shorteners[idx].APIKey
	} else if cfg.Shortener != "" && cfg.ShortenerAPI != "" {
		domain = cfg.Shortener
		apiKey = cfg.ShortenerAPI
	} else {
		return longURL
	}

	var apiURL string
	if strings.Contains(domain, "%s") {
		if strings.Count(domain, "%s") >= 2 {
			apiURL = fmt.Sprintf(domain, apiKey, url.QueryEscape(longURL))
		} else {
			apiURL = fmt.Sprintf(domain, url.QueryEscape(longURL))
		}
	} else if strings.Contains(domain, "{api}") || strings.Contains(domain, "{url}") {
		apiURL = strings.ReplaceAll(domain, "{api}", apiKey)
		apiURL = strings.ReplaceAll(apiURL, "{url}", url.QueryEscape(longURL))
	} else {
		cleanDomain := strings.TrimPrefix(domain, "https://")
		cleanDomain = strings.TrimPrefix(cleanDomain, "http://")
		cleanDomain = strings.TrimSuffix(cleanDomain, "/")

		switch {
		case strings.Contains(cleanDomain, "ouo.io"):
			apiURL = fmt.Sprintf("http://ouo.io/api/%s?s=%s", apiKey, url.QueryEscape(longURL))
		case strings.Contains(cleanDomain, "cutt.ly"):
			apiURL = fmt.Sprintf("https://cutt.ly/api/api.php?key=%s&short=%s", apiKey, url.QueryEscape(longURL))
		default:
			apiURL = fmt.Sprintf("https://%s/api?api=%s&url=%s", cleanDomain, apiKey, url.QueryEscape(longURL))
		}
	}

	resp, err := httpClient.Get(apiURL)
	if err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)

		var data map[string]interface{}
		if json.Unmarshal(b, &data) == nil {
			for _, k := range []string{"shortenedUrl", "shorturl", "url", "short", "link"} {
				if val, ok := data[k].(string); ok && val != "" && strings.HasPrefix(val, "http") {
					return val
				}
			}
			if urlObj, ok := data["url"].(map[string]interface{}); ok {
				if sl, ok := urlObj["shortLink"].(string); ok && sl != "" {
					return sl
				}
			}
		}

		res := strings.TrimSpace(string(b))
		if res != "" && (strings.HasPrefix(res, "http://") || strings.HasPrefix(res, "https://")) {
			return res
		}
	}

	return longURL
}

func CheckingAccess(userID int64, isGroup bool, botUsername string, userCache *types.SafeUserMap) (string, *telegram.ButtonMaker) {
	cfg := config.Get()
	if !cfg.ShortenerEnabled || cfg.TokenTimeout <= 0 {
		return "", nil
	}
	if telegram.IsOwner(userID) || telegram.IsSudo(userID, userCache) || telegram.IsPremium(userID, userCache) {
		return "", nil
	}

	u := userCache.GetOrCreate(userID)
	now := time.Now().Unix()

	if cfg.LoginPass != "" && u.Token == cfg.LoginPass {
		return "", nil
	}

	isExpired := u.TokenTime == 0 || (now-u.TokenTime) > cfg.TokenTimeout
	if !isExpired {
		return "", nil
	}

	token := u.Token
	if token == "" || u.TokenTime > 0 {
		token = uuid.NewString()
		u.Token = token
		u.TokenTime = 0
		_ = DB.SaveUser(u)
	}

	btn := telegram.NewButtonMaker()
	validityStr := GetReadableTime(cfg.TokenTimeout)

	if cfg.TokenInPM && isGroup {
		btn.UButtonStyled("◈ "+telegram.ToSmallCaps("Get Token in PM"), fmt.Sprintf("https://t.me/%s?start=req_%d", botUsername, userID), "primary")
		msg := fmt.Sprintf("<i>Temporary Token has expired.</i> Click below to get token generation link in PM.\n<b>Validity:</b> <code>%s</code>", validityStr)
		return msg, btn
	}

	rawPayload := fmt.Sprintf("%s&&%d", token, userID)
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(rawPayload))
	botStartURL := fmt.Sprintf("https://t.me/%s?start=%s", botUsername, encodedPayload)
	shortenedURL := ShortURL(botStartURL)

	btn.UButtonStyled("◈ "+telegram.ToSmallCaps("Generate New Token"), shortenedURL, "success")
	msg := fmt.Sprintf("<i>Temporary Token has expired.</i> Kindly generate a new token to use the bot.\n<b>Validity:</b> <code>%s</code>", validityStr)
	return msg, btn
}

func VerifyToken(tokenPayload string, senderID int64, userCache *types.SafeUserMap) (bool, string) {
	raw, err := base64.RawURLEncoding.DecodeString(tokenPayload)
	if err != nil {
		raw, err = base64.StdEncoding.DecodeString(tokenPayload)
	}
	if err != nil {
		return false, "Invalid Token Payload!"
	}
	parts := strings.Split(string(raw), "&&")
	if len(parts) != 2 {
		return false, "Malformed Token Payload!"
	}
	token := parts[0]
	u := userCache.GetOrCreate(senderID)
	if u.Token != token {
		return false, "Token mismatch or expired. Please generate a new one."
	}

	u.TokenTime = time.Now().Unix()
	_ = DB.SaveUser(u)
	cfg := config.Get()
	return true, fmt.Sprintf("✓ <b>Token Verified Successfully!</b>\n<b>Validity:</b> <code>%s</code>", GetReadableTime(cfg.TokenTimeout))
}
