package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"azml-go/types"
	"azml-go/helper/ext"
)

type DirectDownloader struct {
	client *http.Client
}

func NewDirectDownloader() *DirectDownloader {
	return &DirectDownloader{
		client: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (d *DirectDownloader) Download(ctx context.Context, link, outPath string, onProgress func(cur, total int64, speed float64, eta string)) error {
	// Primary: Use Aria2c with 16 connections
	aria := GetAria2Client()
	dir, filename := filepath.Split(outPath)
	ariaLink := link
	if strings.HasPrefix(ariaLink, "http://") || strings.HasPrefix(ariaLink, "https://") {
		if strings.Contains(ariaLink, "#") {
			ariaLink += "_" + ext.RandomHex(3)
		} else {
			ariaLink += "#" + ext.RandomHex(3)
		}
	}
	if gid, err := aria.AddURI(ariaLink, dir, filename, nil); err == nil {
		task := NewAria2DownloadTask(gid, filename, dir, 0, 0, 0)
		_, pollErr := task.PollUntilComplete(ctx, func(c, t int64) {
			if onProgress != nil {
				onProgress(c, t, task.Speed(), task.ETA())
			}
		})
		if pollErr == nil {
			return nil
		}
	}

	// Fallback: Native Go streaming download with 64KB pooled buffer
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bad HTTP status: %s", resp.Status)
	}

	_ = os.MkdirAll(filepath.Dir(outPath), 0755)
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength
	var current int64

	buf := make([]byte, 64*1024)
	lastReport := time.Now()
	var lastCurrent int64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return wErr
			}
			current += int64(n)
			if onProgress != nil && time.Since(lastReport) >= 1*time.Second {
				now := time.Now()
				elapsed := now.Sub(lastReport).Seconds()
				speed := float64(0)
				if elapsed > 0 {
					speed = float64(current-lastCurrent) / elapsed
				}
				eta := "-"
				if speed > 0 && total > current {
					rem := total - current
					eta = ext.GetReadableTime(int64(float64(rem) / speed))
				}
				onProgress(current, total, speed, eta)
				lastReport = now
				lastCurrent = current
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return rErr
		}
	}

	if onProgress != nil {
		onProgress(current, total, 0, "-")
	}
	return nil
}

type DirectDownloadTask struct {
	name      string
	size      int64
	curr      int64
	speed     float64
	startTime time.Time
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	mu        sync.RWMutex
}

func (t *DirectDownloadTask) GID() string             { return "direct" }
func (t *DirectDownloadTask) Name() string            { return t.name }
func (t *DirectDownloadTask) Size() int64             { return t.size }
func (t *DirectDownloadTask) DownloadedBytes() int64  { return t.curr }
func (t *DirectDownloadTask) Speed() float64          { return t.speed }
func (t *DirectDownloadTask) Status() types.TaskStatus  { return t.status }
func (t *DirectDownloadTask) Engine() string          { return "Aria2c / Direct HTTP" }
func (t *DirectDownloadTask) StartTime() time.Time    { return t.startTime }
func (t *DirectDownloadTask) MessageID() int          { return 0 }
func (t *DirectDownloadTask) ChatID() int64           { return 0 }
func (t *DirectDownloadTask) UserID() int64           { return 0 }
func (t *DirectDownloadTask) Progress() float64 {
	if t.size <= 0 {
		return 0
	}
	return (float64(t.curr) / float64(t.size)) * 100
}
func (t *DirectDownloadTask) ETA() string {
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	rem := t.size - t.curr
	if rem <= 0 {
		return "0s"
	}
	return ext.GetReadableTime(int64(float64(rem) / t.speed))
}
func (t *DirectDownloadTask) Cancel() error {
	if t.cancelFn != nil {
		t.cancelFn()
	}
	return nil
}

