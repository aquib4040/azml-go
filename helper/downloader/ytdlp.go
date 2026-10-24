package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/types"
)

var (
	ytProgressReg = regexp.MustCompile(`\[download\]\s+(\d+\.?\d*)%\s+of\s+~?(\d+\.?\d*)([KMGTP]?i?B)\s+at\s+(\d+\.?\d*)([KMGTP]?i?B/s)\s+ETA\s+(\d+:\d+)`)
	ytDestReg     = regexp.MustCompile(`\[download\] Destination: (.+)`)
	ytMergerReg   = regexp.MustCompile(`\[Merger\] Merging formats into "(.+)"`)
)

type YtDlpDownloader struct{}

func NewYtDlpDownloader() *YtDlpDownloader {
	return &YtDlpDownloader{}
}

func (y *YtDlpDownloader) GetInfo(ctx context.Context, link string) (title string, isPlaylist bool, count int, err error) {
	cmd := exec.CommandContext(ctx, "yt-dlp", "--dump-single-json", "--flat-playlist", link)
	out, err := cmd.Output()
	if err != nil {
		return "", false, 0, err
	}

	var data struct {
		Title   string        `json:"title"`
		Type    string        `json:"_type"`
		Entries []interface{} `json:"entries"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return "", false, 0, err
	}

	if data.Type == "playlist" || len(data.Entries) > 0 {
		return data.Title, true, len(data.Entries), nil
	}
	return data.Title, false, 1, nil
}

func (y *YtDlpDownloader) Download(ctx context.Context, link, outDir, customFormat string, onProgress func(cur, total int64, speed float64, eta string)) (string, error) {
	_ = os.MkdirAll(outDir, 0755)

	outTmpl := filepath.Join(outDir, "%(title)s.%(ext)s")
	args := []string{
		"-o", outTmpl,
		"--newline",
		"--no-warnings",
		"--no-part",
		"--concurrent-fragments", "16", // Max speed parallel fragment download
	}

	if customFormat != "" {
		args = append(args, "-f", customFormat)
	} else {
		args = append(args, "-f", "bv*+ba/b")
	}

	if _, err := os.Stat("cookies.txt"); err == nil {
		args = append(args, "--cookies", "cookies.txt")
	}

	args = append(args, link)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(stdout)
	var downloadedFile string

	for scanner.Scan() {
		line := scanner.Text()
		if m := ytDestReg.FindStringSubmatch(line); len(m) > 1 {
			downloadedFile = m[1]
		}
		if m := ytMergerReg.FindStringSubmatch(line); len(m) > 1 {
			downloadedFile = m[1]
		}
		if m := ytProgressReg.FindStringSubmatch(line); len(m) > 6 {
			pct, _ := strconv.ParseFloat(m[1], 64)
			sizeVal, _ := strconv.ParseFloat(m[2], 64)
			unit := m[3]
			speedVal, _ := strconv.ParseFloat(m[4], 64)
			eta := m[6]

			totalBytes := parseBytes(sizeVal, unit)
			curBytes := int64((pct / 100.0) * float64(totalBytes))
			speedBytes := float64(parseBytes(speedVal, m[5]))

			if onProgress != nil {
				onProgress(curBytes, totalBytes, speedBytes, eta)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", err
	}

	if downloadedFile != "" {
		return downloadedFile, nil
	}

	// Scan outDir for any created file
	files, _ := os.ReadDir(outDir)
	if len(files) > 0 {
		return filepath.Join(outDir, files[0].Name()), nil
	}

	return "", errors.New("yt-dlp completed but output file not found")
}

func parseBytes(val float64, unit string) int64 {
	unit = strings.ToUpper(unit)
	multiplier := float64(1)
	switch {
	case strings.HasPrefix(unit, "K"):
		multiplier = 1024
	case strings.HasPrefix(unit, "M"):
		multiplier = 1024 * 1024
	case strings.HasPrefix(unit, "G"):
		multiplier = 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "T"):
		multiplier = 1024 * 1024 * 1024 * 1024
	}
	return int64(val * multiplier)
}

type YtDlpDownloadTask struct {
	name      string
	size      int64
	curr      int64
	speed     float64
	eta       string
	startTime time.Time
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	mu        sync.RWMutex
}

func (t *YtDlpDownloadTask) GID() string              { return "ytdlp" }
func (t *YtDlpDownloadTask) Name() string             { return t.name }
func (t *YtDlpDownloadTask) Size() int64              { return t.size }
func (t *YtDlpDownloadTask) DownloadedBytes() int64   { return t.curr }
func (t *YtDlpDownloadTask) Speed() float64           { return t.speed }
func (t *YtDlpDownloadTask) Status() types.TaskStatus { return t.status }
func (t *YtDlpDownloadTask) Engine() string           { return "yt-dlp [16frags]" }
func (t *YtDlpDownloadTask) StartTime() time.Time     { return t.startTime }
func (t *YtDlpDownloadTask) MessageID() int           { return 0 }
func (t *YtDlpDownloadTask) ChatID() int64            { return 0 }
func (t *YtDlpDownloadTask) UserID() int64            { return 0 }
func (t *YtDlpDownloadTask) Progress() float64 {
	if t.size <= 0 {
		return 0
	}
	return (float64(t.curr) / float64(t.size)) * 100
}
func (t *YtDlpDownloadTask) ETA() string {
	if t.eta != "" {
		return t.eta
	}
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	rem := t.size - t.curr
	if rem <= 0 {
		return "0s"
	}
	return ext.GetReadableTime(int64(float64(rem) / t.speed))
}
func (t *YtDlpDownloadTask) Cancel() error {
	if t.cancelFn != nil {
		t.cancelFn()
	}
	return nil
}
