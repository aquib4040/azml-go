package ext

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	urlRegex  = regexp.MustCompile(`(?i)\bhttps?://[^\s()<>]+(?:\([\w\d]+\)|([^[:punct:]\s]|/))`)
	magnetReg = regexp.MustCompile(`(?i)^magnet:\?xt=urn:[a-z0-9]+:[a-z0-9]{32,40}`)
	megaReg   = regexp.MustCompile(`(?i)https?://mega\.nz/(file|folder)/[a-zA-Z0-9_-]+(#|!)[a-zA-Z0-9_-]+`)
	tgLinkReg = regexp.MustCompile(`(?i)https?://t\.me/(c/)?([a-zA-Z0-9_]+)/([0-9]+)`)
)

func GetReadableFileSize(sizeInBytes int64) string {
	if sizeInBytes <= 0 {
		return "0 B"
	}
	const unit = 1024
	if sizeInBytes < unit {
		return fmt.Sprintf("%d B", sizeInBytes)
	}
	div, exp := int64(unit), 0
	for n := sizeInBytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.2f %s", float64(sizeInBytes)/float64(div), units[exp])
}

func GetReadableTime(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	d := seconds / 86400
	h := (seconds % 86400) / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60

	var parts []string
	if d > 0 {
		parts = append(parts, fmt.Sprintf("%dd", d))
	}
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, " ")
}

func GetMD5Hash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := md5.New()
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(hasher, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func IsURL(s string) bool {
	u, err := url.ParseRequestURI(s)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func IsMagnet(s string) bool {
	return magnetReg.MatchString(strings.TrimSpace(s))
}

func IsMegaLink(s string) bool {
	return megaReg.MatchString(strings.TrimSpace(s))
}

func IsTelegramLink(s string) bool {
	return tgLinkReg.MatchString(strings.TrimSpace(s))
}

func SanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "*", "-")
	name = strings.ReplaceAll(name, "?", "-")
	name = strings.ReplaceAll(name, "\"", "-")
	name = strings.ReplaceAll(name, "<", "-")
	name = strings.ReplaceAll(name, ">", "-")
	name = strings.ReplaceAll(name, "|", "-")
	return strings.Trim(name, " .-_")
}

func RandomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ShortHash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:4])
}

func CleanPath(path string) error {
	return os.RemoveAll(path)
}

func GetPathSize(path string) (int64, error) {
	var totalSize int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	return totalSize, err
}

func ParseTagInfo(text, botName string) (tagStr, idStr, cleanedText string) {
	lines := strings.Split(text, "\n")
	cleanedText = text
	if len(lines) == 0 {
		return
	}
	tokens := strings.Fields(lines[0])
	var remaining []string
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "tag:") {
			tagStr = strings.TrimPrefix(tok, "tag:")
		} else if strings.HasPrefix(tok, "id:") {
			idStr = strings.TrimPrefix(tok, "id:")
		} else {
			remaining = append(remaining, tok)
		}
	}
	lines[0] = strings.Join(remaining, " ")
	cleanedText = strings.Join(lines, "\n")
	return
}

func TimeSinceFormatted(t time.Time) string {
	diff := time.Since(t)
	return GetReadableTime(int64(diff.Seconds()))
}
