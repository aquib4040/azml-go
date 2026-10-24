package ext

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/google/uuid"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func ShortURL(longURL string) string {
	cfg := config.Get()
	if !cfg.ShortenerEnabled || cfg.Shortener == "" || cfg.ShortenerAPI == "" {
		return longURL
	}

	domain := strings.TrimPrefix(cfg.Shortener, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimSuffix(domain, "/")

	var apiURL string
	switch {
	case strings.Contains(domain, "ouo.io"):
		apiURL = fmt.Sprintf("http://ouo.io/api/%s?s=%s", cfg.ShortenerAPI, url.QueryEscape(longURL))
		resp, err := httpClient.Get(apiURL)
		if err == nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			res := strings.TrimSpace(string(b))
			if res != "" && strings.HasPrefix(res, "http") {
				return res
			}
		}
	case strings.Contains(domain, "cutt.ly"):
		apiURL = fmt.Sprintf("https://cutt.ly/api/api.php?key=%s&short=%s", cfg.ShortenerAPI, url.QueryEscape(longURL))
		resp, err := httpClient.Get(apiURL)
		if err == nil {
			defer resp.Body.Close()
			var data struct {
				URL struct {
					ShortLink string `json:"shortLink"`
				} `json:"url"`
			}
			if json.NewDecoder(resp.Body).Decode(&data) == nil && data.URL.ShortLink != "" {
				return data.URL.ShortLink
			}
		}
	default:
		apiURL = fmt.Sprintf("https://%s/api?api=%s&url=%s", domain, cfg.ShortenerAPI, url.QueryEscape(longURL))
		resp, err := httpClient.Get(apiURL)
		if err == nil {
			defer resp.Body.Close()
			var data struct {
				ShortenedURL string `json:"shortenedUrl"`
			}
			if json.NewDecoder(resp.Body).Decode(&data) == nil && data.ShortenedURL != "" {
				return data.ShortenedURL
			}
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
		btn.UButton("Get Token in PM", fmt.Sprintf("https://t.me/%s?start=req_%d", botUsername, userID))
		msg := fmt.Sprintf("<i>Temporary Token has expired.</i> Click below to get token generation link in PM.\n<b>Validity:</b> <code>%s</code>", validityStr)
		return msg, btn
	}

	rawPayload := fmt.Sprintf("%s&&%d", token, userID)
	encodedPayload := base64.StdEncoding.EncodeToString([]byte(rawPayload))
	botStartURL := fmt.Sprintf("https://t.me/%s?start=%s", botUsername, encodedPayload)
	shortenedURL := ShortURL(botStartURL)

	btn.UButton("Generate New Token", shortenedURL)
	msg := fmt.Sprintf("<i>Temporary Token has expired.</i> Kindly generate a new token to use the bot.\n<b>Validity:</b> <code>%s</code>", validityStr)
	return msg, btn
}

func VerifyToken(tokenPayload string, senderID int64, userCache *types.SafeUserMap) (bool, string) {
	raw, err := base64.StdEncoding.DecodeString(tokenPayload)
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
	return true, fmt.Sprintf("🎉 <b>Token Verified Successfully!</b>\n<b>Validity:</b> <code>%s</code>", GetReadableTime(cfg.TokenTimeout))
}
