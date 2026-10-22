package downloader

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"azml-go/types"
	"azml-go/config"
	"azml-go/helper/ext"
)

type MegaFileInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Selected bool   `json:"selected"`
}

type MegaDownloader struct {
	email    string
	password string
}

func NewMegaDownloader() *MegaDownloader {
	cfg := config.Get()
	return &MegaDownloader{
		email:    cfg.MegaEmail,
		password: cfg.MegaPassword,
	}
}

func (m *MegaDownloader) Download(ctx context.Context, megaURL, outDir string, onProgress func(cur, total int64)) (string, error) {
	_ = os.MkdirAll(outDir, 0755)

	// Try first via aria2c if it's an exportable stream link, or use mega-get / megadl
	// If mega-get is present in PATH
	if _, err := exec.LookPath("mega-get"); err == nil {
		cmd := exec.CommandContext(ctx, "mega-get", megaURL, outDir)
		if err := cmd.Run(); err == nil {
			// Find downloaded file in outDir
			files, _ := os.ReadDir(outDir)
			if len(files) > 0 {
				return filepath.Join(outDir, files[0].Name()), nil
			}
		}
	}

	// Try megadl (part of megatools)
	if _, err := exec.LookPath("megadl"); err == nil {
		args := []string{"--path", outDir}
		if m.email != "" && m.password != "" {
			args = append(args, "--username", m.email, "--password", m.password)
		}
		args = append(args, megaURL)
		cmd := exec.CommandContext(ctx, "megadl", args...)
		if err := cmd.Run(); err == nil {
			files, _ := os.ReadDir(outDir)
			if len(files) > 0 {
				return filepath.Join(outDir, files[0].Name()), nil
			}
		}
	}

	// Fallback to Aria2c RPC direct download
	aria := GetAria2Client()
	gid, err := aria.AddURI(megaURL, outDir, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to start mega download via aria2c: %v", err)
	}

	task := NewAria2DownloadTask(gid, "Mega Download", outDir, 0, 0, 0)
	return task.PollUntilComplete(ctx, onProgress)
}

type MegaDownloadTask struct {
	name      string
	size      int64
	curr      int64
	speed     float64
	startTime time.Time
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	mu        sync.RWMutex
}

func (t *MegaDownloadTask) GID() string             { return "mega" }
func (t *MegaDownloadTask) Name() string            { return t.name }
func (t *MegaDownloadTask) Size() int64             { return t.size }
func (t *MegaDownloadTask) DownloadedBytes() int64  { return t.curr }
func (t *MegaDownloadTask) Speed() float64          { return t.speed }
func (t *MegaDownloadTask) Status() types.TaskStatus  { return t.status }
func (t *MegaDownloadTask) Engine() string          { return "MegaSDK / Aria2c" }
func (t *MegaDownloadTask) StartTime() time.Time    { return t.startTime }
func (t *MegaDownloadTask) MessageID() int          { return 0 }
func (t *MegaDownloadTask) ChatID() int64           { return 0 }
func (t *MegaDownloadTask) UserID() int64           { return 0 }
func (t *MegaDownloadTask) Progress() float64 {
	if t.size <= 0 {
		return 0
	}
	return (float64(t.curr) / float64(t.size)) * 100
}
func (t *MegaDownloadTask) ETA() string {
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	rem := t.size - t.curr
	if rem <= 0 {
		return "0s"
	}
	return ext.GetReadableTime(int64(float64(rem) / t.speed))
}
func (t *MegaDownloadTask) Cancel() error {
	if t.cancelFn != nil {
		t.cancelFn()
	}
	return nil
}

func IsMegaLink(link string) bool {
	return strings.Contains(link, "mega.nz")
}

