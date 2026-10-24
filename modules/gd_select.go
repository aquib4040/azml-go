package modules

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/web"

	"github.com/gotd/td/tg"
)

type GDriveSelectCacheItem struct {
	Files       []downloader.GDriveFile
	DoneChan    chan bool
	IsCancelled bool
	UserID      int64
}

var (
	gdSelectCache sync.Map
)

func BuildGDriveSelectionButtons(uid string) tg.ReplyMarkupClass {
	cfg := config.Get()
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%d", cfg.BaseURLPort)
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	pinCode := ""
	for _, ch := range uid {
		if unicode.IsDigit(ch) {
			pinCode += string(ch)
			if len(pinCode) == 4 {
				break
			}
		}
	}
	if len(pinCode) < 4 {
		pinCode = "0000"
	}

	b := telegram.NewButtonMaker()
	b.UButtonStyled("📁 "+telegram.ToSmallCaps("Select Files"), fmt.Sprintf("%s/app/files/%s", baseURL, uid), "primary")
	b.IButtonStyled("🔢 "+telegram.ToSmallCaps("Pincode"), fmt.Sprintf("gdsel pin %s %s", uid, pinCode), "primary")
	b.IButtonStyled("× "+telegram.ToSmallCaps("Cancel"), fmt.Sprintf("gdsel cancel %s", uid), "danger")
	b.IButtonStyled("✓ "+telegram.ToSmallCaps("Done Selecting"), fmt.Sprintf("gdsel done %s", uid), "success")
	return b.BuildMenu(2)
}

func RegisterGDriveSelectCache(uid string, files []downloader.GDriveFile, userID int64) chan bool {
	doneChan := make(chan bool, 1)
	gdSelectCache.Store(uid, &GDriveSelectCacheItem{
		Files:    files,
		DoneChan: doneChan,
		UserID:   userID,
	})
	web.GDriveStore.SaveData(uid, files)
	return doneChan
}

func GetGDriveSelectCache(uid string) *GDriveSelectCacheItem {
	if val, ok := gdSelectCache.Load(uid); ok {
		return val.(*GDriveSelectCacheItem)
	}
	return nil
}

func ClearGDriveSelectCache(uid string) {
	gdSelectCache.Delete(uid)
	web.GDriveStore.DeleteData(uid)
}

func HandleGDriveSelectCallback(ctx context.Context, rawAPI *tg.Client, sender *telegram.MessageSender, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		return
	}
	action := parts[1]
	uid := parts[2]

	cache := GetGDriveSelectCache(uid)
	if cache == nil {
		_ = sender.AnswerCallback(ctx, queryID, "⚠️ Session expired or download already started!", true)
		_ = sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}

	if fromUID != 0 && fromUID != cache.UserID {
		_ = sender.AnswerCallback(ctx, queryID, "❌ This selection is not for you!", true)
		return
	}

	switch action {
	case "pin":
		pincode := ""
		if len(parts) > 3 {
			pincode = parts[3]
		}
		_ = sender.AnswerCallback(ctx, queryID, fmt.Sprintf("🔑 PIN: %s", pincode), true)

	case "done":
		selectedIDs := web.GDriveStore.ReadSelection(uid)
		var selected []downloader.GDriveFile
		for i := range cache.Files {
			cache.Files[i].Selected = containsString(selectedIDs, cache.Files[i].ID)
			if cache.Files[i].Selected && !cache.Files[i].IsDir {
				selected = append(selected, cache.Files[i])
			}
		}

		if len(selected) == 0 {
			_ = sender.AnswerCallback(ctx, queryID, "⚠️ Select at least one file first!", true)
			return
		}

		var totalSize int64
		for _, f := range selected {
			totalSize += f.Size
		}

		_ = sender.AnswerCallback(ctx, queryID, fmt.Sprintf("🚀 Starting download of %d file(s) (%s)!", len(selected), ext.GetReadableFileSize(totalSize)), false)
		_ = sender.DeleteMessage(ctx, peer, []int{msgID})

		select {
		case cache.DoneChan <- true:
		default:
		}

	case "cancel":
		cache.IsCancelled = true
		_ = sender.AnswerCallback(ctx, queryID, "❌ Download cancelled!", false)
		_ = sender.DeleteMessage(ctx, peer, []int{msgID})
		select {
		case cache.DoneChan <- false:
		default:
		}
		ClearGDriveSelectCache(uid)
	}
}
