package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type MediaInfoModule struct {
	sender *telegram.MessageSender
}

func NewMediaInfoModule(sender *telegram.MessageSender) *MediaInfoModule {
	return &MediaInfoModule{sender: sender}
}

func (m *MediaInfoModule) HandleMediaInfo(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	link := ""
	parts := strings.Fields(msg.Text)
	if len(parts) > 1 {
		link = parts[1]
	} else if msg.IsReply && msg.ReplyText != "" {
		link = strings.TrimSpace(msg.ReplyText)
	}

	if link == "" {
		helpMsg := "<b>MediaInfo Usage:</b>\n" +
			"• <code>/mediainfo [download link]</code>\n" +
			"• Reply to a download link with <code>/mediainfo</code>"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, nil)
		return
	}

	waitID, _ := m.sender.SendMessage(ctx, peer, "<i>Generating MediaInfo...</i>", msg.MessageID, nil)

	go func() {
		tmpDir, err := os.MkdirTemp("", "mediainfo_*")
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, "Error creating temp dir", nil)
			return
		}
		defer os.RemoveAll(tmpDir)

		destFile := filepath.Join(tmpDir, "sample_media")

		// Download first 10MB chunk for probe
		client := &http.Client{Timeout: 30 * time.Second}
		req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("Error request: %v", err), nil)
			return
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Range", "bytes=0-1048576") // 1 MB chunk for fast probe

		resp, err := client.Do(req)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("Failed to fetch media: %v", err), nil)
			return
		}
		defer resp.Body.Close()

		f, err := os.Create(destFile)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, "Failed to create temp file", nil)
			return
		}
		_, _ = io.CopyN(f, resp.Body, 1024*1024)
		_ = f.Close()

		fileName := filepath.Base(link)
		if u, err := url.Parse(link); err == nil {
			fileName = filepath.Base(u.Path)
		}
		if fileName == "" || fileName == "/" || fileName == "." {
			fileName = "Media_File"
		}

		infoText := runMediaInfo(destFile)
		formattedHTML := formatMediaInfoHTML(fileName, infoText)

		pageURL, err := createTelegraphPage("MediaInfo X", formattedHTML)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("<b>MediaInfo Output:</b>\n<pre>%s</pre>", truncateString(infoText, 3000)), nil)
			return
		}

		respText := fmt.Sprintf("<b>MediaInfo:</b>\n\n➲ <b>Link :</b> %s", pageURL)
		_ = m.sender.EditMessage(ctx, peer, waitID, respText, nil)
	}()
}

func runMediaInfo(filePath string) string {
	if _, err := exec.LookPath("mediainfo"); err == nil {
		cmd := exec.Command("mediainfo", filePath)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.String()
		}
	}

	// Fallback to ffprobe
	if _, err := exec.LookPath("ffprobe"); err == nil {
		cmd := exec.Command("ffprobe", "-hide_banner", filePath)
		var out bytes.Buffer
		cmd.Stderr = &out
		if err := cmd.Run(); err == nil || out.Len() > 0 {
			return out.String()
		}
	}

	meta, _ := ext.ProbeMedia(filePath)
	if meta != nil {
		return fmt.Sprintf("Video: %s\nDuration: %d s\nResolution: %dx%d\nAudio: %s\nQuality: %s",
			meta.VideoCodec, meta.Duration, meta.Width, meta.Height, meta.AudioCodec, meta.Quality)
	}

	return "No MediaInfo could be extracted."
}

func formatMediaInfoHTML(fileName, out string) string {
	sections := map[string]string{
		"General":  "◈",
		"Video":    "◈",
		"Audio":    "◈",
		"Text":     "◈",
		"Subtitle": "◈",
		"Menu":     "◈",
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<h4>◈ %s</h4><br>", fileName))

	lines := strings.Split(out, "\n")
	inPre := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := false
		for sec, emoji := range sections {
			if strings.HasPrefix(trimmed, sec) {
				if inPre {
					sb.WriteString("</pre><br>")
				}
				sb.WriteString(fmt.Sprintf("<h4>%s %s</h4><br><pre>", emoji, trimmed))
				inPre = true
				isHeader = true
				break
			}
		}
		if !isHeader {
			if !inPre {
				sb.WriteString("<pre>")
				inPre = true
			}
			sb.WriteString(line + "\n")
		}
	}

	if inPre {
		sb.WriteString("</pre>")
	}
	return sb.String()
}

func createTelegraphPage(title, htmlContent string) (string, error) {
	apiURL := "https://api.graph.org/createPage"

	type telegraphNode struct {
		Tag      string          `json:"tag"`
		Children []interface{}   `json:"children,omitempty"`
		Attrs    json.RawMessage `json:"attrs,omitempty"`
	}

	// Create page with Telegraph / Graph.org API
	postData := url.Values{}
	postData.Set("access_token", "d3b25feccb89e508a9114afb82aa421fe2a9712b963b387cc62ac036ee2b")
	postData.Set("title", title)
	postData.Set("author_name", "AZML-Go")
	postData.Set("author_url", "https://t.me/CANON_BOTS")

	contentNodes := []telegraphNode{
		{
			Tag:      "p",
			Children: []interface{}{htmlContent},
		},
	}
	contentJSON, _ := json.Marshal(contentNodes)
	postData.Set("content", string(contentJSON))
	postData.Set("return_content", "false")

	resp, err := http.PostForm(apiURL, postData)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.OK {
		if result.Result.URL != "" {
			return result.Result.URL, nil
		}
		return "https://graph.org/" + result.Result.Path, nil
	}

	return "", fmt.Errorf("telegraph create page failed")
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
