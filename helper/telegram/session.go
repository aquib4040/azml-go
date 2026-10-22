package telegram

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/dcs"
)

// ParseSessionString parses Pyrogram (v1, v2), Pyrofork, Pyrotgfork, Telethon, or gotd session strings
func ParseSessionString(ctx context.Context, sessionStr string) (session.Storage, error) {
	sessionStr = strings.TrimSpace(sessionStr)
	if sessionStr == "" {
		return nil, fmt.Errorf("empty session string")
	}

	mem := &session.StorageMemory{}
	loader := session.Loader{Storage: mem}

	// 1. Try gotd JSON session
	if strings.HasPrefix(sessionStr, "{") && strings.HasSuffix(sessionStr, "}") {
		var sData session.Data
		if err := json.Unmarshal([]byte(sessionStr), &sData); err == nil && len(sData.AuthKey) > 0 {
			if sData.Addr == "" && sData.DC > 0 {
				sData.Addr = getPrimaryDCAddr(sData.DC)
			}
			if err := loader.Save(ctx, &sData); err == nil {
				return mem, nil
			}
		}
	}

	// 2. Try Telethon session
	if data, err := session.TelethonSession(sessionStr); err == nil && data != nil {
		if data.Addr == "" && data.DC > 0 {
			data.Addr = getPrimaryDCAddr(data.DC)
		}
		if err := loader.Save(ctx, data); err == nil {
			return mem, nil
		}
	}

	// 3. Try Pyrogram / Pyrofork / Pyrotgfork Session Strings (Base64)
	cleaned := sessionStr
	if pad := len(cleaned) % 4; pad != 0 {
		cleaned += strings.Repeat("=", 4-pad)
	}

	var rawBytes []byte
	var err error
	rawBytes, err = base64.URLEncoding.DecodeString(cleaned)
	if err != nil {
		rawBytes, err = base64.StdEncoding.DecodeString(cleaned)
	}

	if err == nil && len(rawBytes) >= 256 {
		dcID := 2
		var authKey []byte

		switch {
		// Pyrogram v2 & Pyrofork: 267 bytes (>B?256sQ?)
		case len(rawBytes) == 267:
			dcID = int(rawBytes[0])
			authKey = rawBytes[2:258]

		// Pyrogram v1: 263 bytes (>B?256sI?)
		case len(rawBytes) == 263:
			dcID = int(rawBytes[0])
			authKey = rawBytes[2:258]

		// Pyrotgfork / forks with API_ID: 271 bytes (>BI?256sQ?)
		case len(rawBytes) == 271:
			dcID = int(rawBytes[0])
			authKey = rawBytes[6:262]

		// Fallback general 256-byte slice extraction
		case len(rawBytes) >= 258:
			if rawBytes[0] >= 1 && rawBytes[0] <= 5 {
				dcID = int(rawBytes[0])
				authKey = rawBytes[2:258]
			} else {
				authKey = rawBytes[:256]
			}
		}

		if len(authKey) == 256 {
			if dcID < 1 || dcID > 5 {
				dcID = 2
			}

			// Compute Telegram MTProto AuthKeyID: lower 64 bits of SHA1(authKey)
			h := sha1.Sum(authKey)
			authKeyID := h[12:20]

			// Resolve DC address using Telegram production DCs with ipv6=false (matching Pyrogram)
			addr := getPrimaryDCAddr(dcID)

			sData := &session.Data{
				DC:        dcID,
				Addr:      addr,
				AuthKey:   authKey,
				AuthKeyID: authKeyID,
			}

			if err := loader.Save(ctx, sData); err == nil {
				return mem, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to decode session string")
}

// getPrimaryDCAddr looks up the primary IPv4 address for dcID from Telegram production DCs list (ipv6=false)
func getPrimaryDCAddr(dcID int) string {
	prodDCs := dcs.Prod().Options
	primary := dcs.FindPrimaryDCs(prodDCs, dcID, false) // preferIPv6 = false (like Pyrogram)
	if len(primary) > 0 {
		return net.JoinHostPort(primary[0].IPAddress, strconv.Itoa(primary[0].Port))
	}
	return ""
}
