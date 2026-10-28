package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
)

var (
	tgPrivateLink = regexp.MustCompile(`(?i)https?://t\.me/c/(\d+)/(\d+)`)
	tgPublicLink  = regexp.MustCompile(`(?i)https?://t\.me/([a-zA-Z0-9_]+)/(\d+)`)
	mediafireReg  = regexp.MustCompile(`(?i)https?://(?:www\.)?mediafire\.com/(?:file|download)/[a-zA-Z0-9]+(?:/[^/\s]+)?`)
	pixelDrainReg = regexp.MustCompile(`(?i)https?://pixeldrain\.com/u/([a-zA-Z0-9_-]+)`)
)

type ddlReq struct {
	ChannelID int64 `json:"channel_id"`
	MessageID int   `json:"message_id"`
}

type ddlResp struct {
	Success      bool   `json:"success"`
	DownloadLink string `json:"download_link"`
	FileName     string `json:"file_name"`
	FileSize     string `json:"file_size"`
	Error        string `json:"error"`
}

// TryDDLAPI contacts the DDL API service to get a fast direct link for a Telegram message/file
func TryDDLAPI(ctx context.Context, channelID int64, messageID int) (string, error) {
	cfg := config.Get()
	apiBase := strings.TrimRight(strings.TrimSpace(cfg.DDLAPI), "/")
	if apiBase == "" {
		return "", nil
	}

	payload, err := json.Marshal(ddlReq{
		ChannelID: channelID,
		MessageID: messageID,
	})
	if err != nil {
		return "", err
	}

	reqURL := apiBase + "/api/generate_link"
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ddl api request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ddl api returned status %d", resp.StatusCode)
	}

	var data ddlResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("ddl api json decode error: %v", err)
	}

	if data.Success && data.DownloadLink != "" {
		return data.DownloadLink, nil
	}

	return "", fmt.Errorf("ddl api returned unsuccessful: %s", data.Error)
}

// ParseTelegramLink extracts channel ID and message ID from Telegram links
func ParseTelegramLink(link string) (channelID int64, messageID int, ok bool) {
	if m := tgPrivateLink.FindStringSubmatch(link); len(m) == 3 {
		cid, _ := strconv.ParseInt(m[1], 10, 64)
		mid, _ := strconv.Atoi(m[2])
		// Telegram supergroup IDs in private links are prefixed with -100
		if cid > 0 {
			cid = -1000000000000 - cid
		}
		return cid, mid, true
	}
	if m := tgPublicLink.FindStringSubmatch(link); len(m) == 3 {
		mid, _ := strconv.Atoi(m[2])
		return 0, mid, true
	}
	return 0, 0, false
}

// GenerateDirectLink converts various file host URLs into direct download links
func GenerateDirectLink(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, nil
	}

	host := strings.ToLower(u.Host)

	// Pixeldrain
	if strings.Contains(host, "pixeldrain.com") {
		if m := pixelDrainReg.FindStringSubmatch(rawURL); len(m) == 2 {
			return fmt.Sprintf("https://pixeldrain.com/api/file/%s", m[1]), nil
		}
	}

	// Mediafire
	if strings.Contains(host, "mediafire.com") {
		return extractMediafireLink(rawURL)
	}

	// Solidfiles
	if strings.Contains(host, "solidfiles.com") {
		return extractSolidfilesLink(rawURL)
	}

	// GoFile
	if strings.Contains(host, "gofile.io") {
		return extractGofileLink(rawURL)
	}

	return rawURL, nil
}

func extractGofileLink(rawURL string) (string, error) {
	password := ""
	if strings.Contains(rawURL, "::") {
		parts := strings.Split(rawURL, "::")
		rawURL = parts[0]
		if len(parts) > 1 {
			password = parts[1]
		}
	}

	client := &http.Client{Timeout: 20 * time.Second}

	// 1. Try gofileby bypass API first (matching Python implementation)
	bypassURL := fmt.Sprintf("https://gofileby.vercel.app/api/gofile?url=%s", url.QueryEscape(rawURL))
	if password != "" {
		bypassURL += fmt.Sprintf("&password=%s", url.QueryEscape(password))
	}

	if req, err := http.NewRequest("GET", bypassURL, nil); err == nil {
		req.Header.Set("User-Agent", "Mozilla/5.0")
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var res struct {
					Status string `json:"status"`
					Data   []struct {
						Link string `json:"link"`
						Name string `json:"name"`
						Size int64  `json:"size"`
					} `json:"data"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && res.Status == "success" && len(res.Data) > 0 {
					return res.Data[0].Link, nil
				}
			}
		}
	}

	// 2. Direct GoFile API fallback
	contentID := ""
	re := regexp.MustCompile(`(?i)https?://gofile\.io/d/([a-zA-Z0-9_-]+)`)
	if match := re.FindStringSubmatch(rawURL); len(match) == 2 {
		contentID = match[1]
	}
	if contentID == "" {
		return rawURL, fmt.Errorf("invalid gofile URL")
	}

	// Create or fetch guest token
	tokenReq, _ := http.NewRequest("POST", "https://api.gofile.io/accounts", nil)
	tokenReq.Header.Set("User-Agent", "Mozilla/5.0")
	var token string
	if tResp, err := client.Do(tokenReq); err == nil {
		defer tResp.Body.Close()
		var tData struct {
			Status string `json:"status"`
			Data   struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if err := json.NewDecoder(tResp.Body).Decode(&tData); err == nil && tData.Status == "ok" {
			token = tData.Data.Token
		}
	}

	contentURL := fmt.Sprintf("https://api.gofile.io/contents/%s?wt=4fd6sg89d7s6", contentID)
	if token != "" {
		contentURL += fmt.Sprintf("&token=%s", token)
	}
	if password != "" {
		contentURL += fmt.Sprintf("&password=%s", url.QueryEscape(password))
	}

	cReq, err := http.NewRequest("GET", contentURL, nil)
	if err != nil {
		return rawURL, err
	}
	cReq.Header.Set("User-Agent", "Mozilla/5.0")
	cResp, err := client.Do(cReq)
	if err != nil {
		return rawURL, err
	}
	defer cResp.Body.Close()

	var cData struct {
		Status string `json:"status"`
		Data   struct {
			Children map[string]struct {
				Link string `json:"link"`
				Name string `json:"name"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.NewDecoder(cResp.Body).Decode(&cData); err == nil && cData.Status == "ok" {
		for _, child := range cData.Data.Children {
			if child.Link != "" {
				return child.Link, nil
			}
		}
	}

	return rawURL, fmt.Errorf("failed to extract gofile direct download link")
}

func extractMediafireLink(link string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return link, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return link, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return link, err
	}

	re := regexp.MustCompile(`(?i)href\s*=\s*["'](https?://download\d+\.mediafire\.com/[^"']+)["']`)
	if match := re.FindSubmatch(body); len(match) > 1 {
		return string(match[1]), nil
	}

	re2 := regexp.MustCompile(`(?i)aria-label=["']Download file["']\s+href=["'](https?://[^"']+)["']`)
	if match2 := re2.FindSubmatch(body); len(match2) > 1 {
		return string(match2[1]), nil
	}

	return link, nil
}

func extractSolidfilesLink(link string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return link, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := client.Do(req)
	if err != nil {
		return link, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return link, err
	}

	re := regexp.MustCompile(`"downloadUrl":\s*"([^"]+)"`)
	if match := re.FindSubmatch(body); len(match) > 1 {
		return string(match[1]), nil
	}

	return link, nil
}

// ParseContentDispositionFilename extracts the real filename from a Content-Disposition header string
func ParseContentDispositionFilename(cd string) string {
	if cd == "" {
		return ""
	}

	// 1. Check for RFC 5987 / RFC 6266 filename*=UTF-8''... (highest precedence)
	reUTF8 := regexp.MustCompile(`(?i)filename\*=(?:UTF-8|utf-8)''([^;\s]+)`)
	if match := reUTF8.FindStringSubmatch(cd); len(match) > 1 {
		raw := match[1]
		if unescaped, err := url.QueryUnescape(raw); err == nil && unescaped != "" {
			clean := filepath.Base(strings.Trim(unescaped, "\"' "))
			if clean != "" && clean != "." && clean != "/" {
				return clean
			}
		}
	}

	// 2. Check standard mime.ParseMediaType
	if _, params, err := mime.ParseMediaType(cd); err == nil {
		if fn, ok := params["filename"]; ok && fn != "" {
			clean := filepath.Base(strings.Trim(fn, "\"' "))
			if clean != "" && clean != "." && clean != "/" {
				return clean
			}
		}
	}

	// 3. Fallback regex for filename="..." or filename='...' or filename=...
	reGeneric := regexp.MustCompile(`(?i)filename\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s;]+))`)
	if match := reGeneric.FindStringSubmatch(cd); len(match) > 0 {
		for i := 1; i < len(match); i++ {
			if match[i] != "" {
				clean := filepath.Base(strings.Trim(match[i], "\"' "))
				if unescaped, err := url.QueryUnescape(clean); err == nil && unescaped != "" {
					clean = unescaped
				}
				if clean != "" && clean != "." && clean != "/" {
					return clean
				}
			}
		}
	}

	return ""
}

// MimeToExtension maps common media and archive MIME types to their standard file extension
func MimeToExtension(cType string) string {
	switch cType {
	case "video/x-matroska":
		return ".mkv"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "video/x-msvideo":
		return ".avi"
	case "video/x-flv":
		return ".flv"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "application/zip":
		return ".zip"
	case "application/x-rar-compressed", "application/vnd.rar", "application/x-rar":
		return ".rar"
	case "application/x-7z-compressed":
		return ".7z"
	case "application/x-tar":
		return ".tar"
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

// ResolveDownloadMetadata probes a remote URL via a lightweight Range GET request (or HEAD fallback)
// to extract the true filename, final redirected URL, and content size from response headers.
func ResolveDownloadMetadata(ctx context.Context, rawURL string) (string, string, int64, error) {
	if !IsURL(rawURL) {
		return "", rawURL, 0, nil
	}

	client := &http.Client{
		Timeout: 7 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", rawURL, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		// Fallback: try HEAD request if range GET failed
		hReq, hErr := http.NewRequestWithContext(ctx, "HEAD", rawURL, nil)
		if hErr != nil {
			return "", rawURL, 0, err
		}
		hReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
		resp, err = client.Do(hReq)
		if err != nil {
			return "", rawURL, 0, err
		}
	}
	defer resp.Body.Close()

	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	var totalSize int64
	// Check Content-Range: bytes 0-0/1473401238
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		parts := strings.Split(cr, "/")
		if len(parts) == 2 {
			totalSize, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		}
	}
	if totalSize <= 0 && resp.ContentLength > 0 && resp.StatusCode == http.StatusOK {
		totalSize = resp.ContentLength
	}

	// 1. Extract filename from Content-Disposition header
	cd := resp.Header.Get("Content-Disposition")
	filename := ParseContentDispositionFilename(cd)

	// 2. If no filename in Content-Disposition, check final URL path
	if filename == "" && resp.Request != nil && resp.Request.URL != nil {
		urlPath := resp.Request.URL.Path
		cleanBase := filepath.Base(urlPath)
		if strings.Contains(cleanBase, ".") && cleanBase != "." && cleanBase != "/" {
			filename = cleanBase
		}
	}

	// 3. If filename has no extension or is missing, map Content-Type
	cType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(cType, ";") {
		cType = strings.TrimSpace(strings.Split(cType, ";")[0])
	}

	mimeExt := MimeToExtension(cType)
	if filename != "" {
		if filepath.Ext(filename) == "" && mimeExt != "" {
			filename += mimeExt
		}
	}

	return filename, finalURL, totalSize, nil
}
