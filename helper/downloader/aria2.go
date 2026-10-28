package downloader

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/types"
)

type Aria2RPCClient struct {
	rpcURL     string
	secret     string
	httpClient *http.Client
	cmd        *exec.Cmd
	started    bool
	mu         sync.Mutex
}

type Aria2File struct {
	Index           string `json:"index"`
	Path            string `json:"path"`
	Length          string `json:"length"`
	CompletedLength string `json:"completedLength"`
	Selected        string `json:"selected"`
}

type Aria2BTInfo struct {
	Info struct {
		Name string `json:"name"`
	} `json:"info"`
}

type Aria2StatusResponse struct {
	GID             string       `json:"gid"`
	Status          string       `json:"status"` // "active", "waiting", "paused", "error", "complete", "removed"
	TotalLength     string       `json:"totalLength"`
	CompletedLength string       `json:"completedLength"`
	UploadSpeed     string       `json:"uploadSpeed"`
	DownloadSpeed   string       `json:"downloadSpeed"`
	ErrorMessage    string       `json:"errorMessage"`
	Dir             string       `json:"dir"`
	Files           []Aria2File  `json:"files"`
	FollowedBy      []string     `json:"followedBy"`
	Following       string       `json:"following"`
	Bittorrent      *Aria2BTInfo `json:"bittorrent"`
}

type JSONRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPC string               `json:"jsonrpc"`
	ID      string               `json:"id"`
	Result  json.RawMessage      `json:"result"`
	Error   *JSONRPCErrorPayload `json:"error"`
}

type JSONRPCErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var (
	Aria2Client *Aria2RPCClient
	aria2Once   sync.Once
)

func getAria2Port() int {
	if val := os.Getenv("ARIA2_PORT"); val != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && p > 0 {
			return p
		}
	}
	return 6810
}

func GetAria2Client() *Aria2RPCClient {
	aria2Once.Do(func() {
		port := getAria2Port()
		Aria2Client = &Aria2RPCClient{
			rpcURL: fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port),
			httpClient: &http.Client{
				Timeout: 10 * time.Second,
			},
		}
		_ = Aria2Client.EnsureStarted()
	})
	return Aria2Client
}

func findAriaBinary() string {
	candidates := []string{
		"studymaterial",
		"/app/.apt/usr/bin/studymaterial",
		"/usr/bin/studymaterial",
		"aria2c",
		"/app/.apt/usr/bin/aria2c",
		"/app/.apt/bin/aria2c",
		"/usr/bin/aria2c",
		"/usr/local/bin/aria2c",
		"/tmp/aria2c",
	}

	for _, cand := range candidates {
		if strings.Contains(cand, "/") {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				_ = os.Chmod(cand, 0755)
				return cand
			}
		} else {
			if p, err := exec.LookPath(cand); err == nil {
				return p
			}
		}
	}

	if runtime.GOOS == "linux" {
		targetPath := "/tmp/aria2c"
		if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() {
			_ = os.Chmod(targetPath, 0755)
			return targetPath
		}
		if err := autoInstallStaticAria2(targetPath); err == nil {
			return targetPath
		}
	}

	return "aria2c"
}

func autoInstallStaticAria2(targetPath string) error {
	urls := []string{
		"https://github.com/P3TERX/Aria2-Pro-Core/releases/download/1.36.0_2021.08.22/aria2-1.36.0-static-linux-amd64.tar.gz",
	}

	var lastErr error
	client := &http.Client{Timeout: 90 * time.Second}

	for _, u := range urls {
		log.Printf("[Aria2c] Auto-installing static aria2c binary from %s to %s...", u, targetPath)
		resp, err := client.Get(u)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}

		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			lastErr = err
			continue
		}

		tarReader := tar.NewReader(gzReader)
		found := false
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				lastErr = err
				break
			}
			if (header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA) && filepath.Base(header.Name) == "aria2c" {
				outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
				if err != nil {
					lastErr = err
					break
				}
				_, err = io.Copy(outFile, tarReader)
				_ = outFile.Close()
				if err != nil {
					lastErr = err
					break
				}
				_ = os.Chmod(targetPath, 0755)
				found = true
				log.Printf("[Aria2c] Static aria2c binary successfully installed to %s", targetPath)
				break
			}
		}
		_ = gzReader.Close()
		_ = resp.Body.Close()

		if found {
			return nil
		}
	}

	if lastErr != nil {
		log.Printf("[Aria2c] Auto-install failed: %v", lastErr)
		return lastErr
	}
	return errors.New("static binary not found in archive")
}

func (a *Aria2RPCClient) EnsureStarted() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	port := getAria2Port()

	// Check if already running on this port
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "ping",
		Method:  "aria2.getVersion",
		Params:  []interface{}{},
	}
	if _, err := a.rawCall(req); err == nil {
		a.started = true
		return nil
	}

	// Start aria2c / studymaterial daemon with max connection and speed optimizations
	binName := findAriaBinary()
	defaultTrackers := "udp://tracker.opentrackr.org:1337/announce," +
		"udp://open.stealth.si:80/announce," +
		"udp://tracker.torrent.eu.org:451/announce," +
		"udp://tracker.openbittorrent.com:6969/announce," +
		"udp://explodie.org:6969/announce," +
		"udp://tracker.moeking.me:6969/announce," +
		"udp://p4p.arenabg.com:1337/announce," +
		"udp://tracker.tiny-vps.com:6969/announce," +
		"http://tracker.opentrackr.org:1337/announce," +
		"http://tracker.openbittorrent.com:80/announce"

	cmd := exec.Command(binName,
		"--enable-rpc",
		"--rpc-listen-all=true",
		fmt.Sprintf("--rpc-listen-port=%d", port),
		"--max-connection-per-server=16",
		"--split=16",
		"--min-split-size=1M",
		"--max-concurrent-downloads=10",
		"--piece-length=1M",
		"--allow-overwrite=true",
		"--auto-file-renaming=true",
		"--summary-interval=1",
		"--disk-cache=32M",
		"--bt-max-peers=64",
		"--max-upload-limit=1K",
		"--bt-request-peer-speed-limit=0",
		"--seed-time=0",
		"--seed-ratio=0.0",
		"--check-certificate=false",
		"--bt-require-crypto=true",
		"--bt-min-crypto-level=arc4",
		"--enable-dht=true",
		fmt.Sprintf("--dht-listen-port=%d", port+80),
		"--bt-enable-lpd=true",
		"--follow-torrent=mem",
		"--bt-tracker="+defaultTrackers,
		"--peer-id-prefix=-qB4520-",
		"--peer-agent=qBittorrent/4.5.2",
		"--content-disposition-default-utf8=true",
		"--daemon=true",
	)

	if err := cmd.Run(); err != nil {
		log.Printf("[Aria2c] Failed to start daemon (%s): %v", binName, err)
		return err
	}

	for i := 0; i < 10; i++ {
		time.Sleep(300 * time.Millisecond)
		if _, err := a.rawCall(req); err == nil {
			a.started = true
			log.Printf("[Aria2c] Daemon successfully started on port %d", port)
			return nil
		}
	}
	log.Printf("[Aria2c] Warning: daemon process launched but not yet responding on port %d", port)
	return nil
}

func (a *Aria2RPCClient) rawCall(req JSONRPCRequest) (*JSONRPCResponse, error) {
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := a.httpClient.Post(a.rpcURL, "application/json", bytes.NewReader(reqData))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("aria2 error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return &rpcResp, nil
}

func (a *Aria2RPCClient) call(req JSONRPCRequest) (*JSONRPCResponse, error) {
	res, err := a.rawCall(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "connect:") {
			log.Printf("[Aria2c] RPC unreachable (%v), attempting to restart daemon...", err)
			_ = a.EnsureStarted()
			return a.rawCall(req)
		}
		return nil, err
	}
	return res, nil
}

func (a *Aria2RPCClient) AddURI(link, downloadDir, filename string, headers []string) (string, error) {
	_ = os.MkdirAll(downloadDir, 0755)

	opts := map[string]interface{}{
		"dir":                              downloadDir,
		"max-connection-per-server":        "16",
		"split":                            "16",
		"min-split-size":                   "1M",
		"allow-overwrite":                  "true",
		"check-certificate":                "false",
		"bt-max-peers":                     "64",
		"max-upload-limit":                 "1K",
		"bt-request-peer-speed-limit":      "0",
		"seed-time":                        "0",
		"seed-ratio":                       "0.0",
		"follow-torrent":                   "mem",
		"content-disposition-default-utf8": "true",
	}

	if filename != "" {
		opts["out"] = filename
	}
	if len(headers) > 0 {
		opts["header"] = headers
	}

	params := []interface{}{
		[]string{link},
		opts,
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      strconv.FormatInt(time.Now().UnixNano(), 10),
		Method:  "aria2.addUri",
		Params:  params,
	}

	resp, err := a.call(req)
	if err != nil {
		return "", err
	}

	var gid string
	if err := json.Unmarshal(resp.Result, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

func (a *Aria2RPCClient) AddTorrent(torrentFilePath, downloadDir string) (string, error) {
	data, err := os.ReadFile(torrentFilePath)
	if err != nil {
		return "", err
	}

	b64Torrent := base64.StdEncoding.EncodeToString(data)
	opts := map[string]interface{}{
		"dir":                         downloadDir,
		"max-connection-per-server":   "16",
		"split":                       "16",
		"min-split-size":              "1M",
		"bt-max-peers":                "64",
		"max-upload-limit":            "1K",
		"bt-request-peer-speed-limit": "0",
		"seed-time":                   "0",
		"seed-ratio":                  "0.0",
		"allow-overwrite":             "true",
	}

	params := []interface{}{
		b64Torrent,
		[]string{},
		opts,
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      strconv.FormatInt(time.Now().UnixNano(), 10),
		Method:  "aria2.addTorrent",
		Params:  params,
	}

	resp, err := a.call(req)
	if err != nil {
		return "", err
	}

	var gid string
	if err := json.Unmarshal(resp.Result, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

func (a *Aria2RPCClient) TellStatus(gid string) (*Aria2StatusResponse, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      strconv.FormatInt(time.Now().UnixNano(), 10),
		Method:  "aria2.tellStatus",
		Params:  []interface{}{gid},
	}

	resp, err := a.call(req)
	if err != nil {
		return nil, err
	}

	var status Aria2StatusResponse
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (a *Aria2RPCClient) Cancel(gid string) error {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "forceRemove",
		Method:  "aria2.forceRemove",
		Params:  []interface{}{gid},
	}
	_, err := a.call(req)
	return err
}

func (a *Aria2RPCClient) ChangeOption(gid string, options map[string]interface{}) error {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      strconv.FormatInt(time.Now().UnixNano(), 10),
		Method:  "aria2.changeOption",
		Params:  []interface{}{gid, options},
	}
	_, err := a.call(req)
	return err
}

type Aria2DownloadTask struct {
	gid          string
	name         string
	size         int64
	downloaded   int64
	speed        float64
	status       types.TaskStatus
	startTime    time.Time
	messageID    int
	chatID       int64
	userID       int64
	client       *Aria2RPCClient
	downloadDir  string
	downloadPath string
	mu           sync.RWMutex
}

func NewAria2DownloadTask(gid, name, downloadDir string, msgID int, chatID, userID int64) *Aria2DownloadTask {
	cleanName := name
	if cleanName != "" {
		cleanName = filepath.Base(cleanName)
	}
	return &Aria2DownloadTask{
		gid:         gid,
		name:        cleanName,
		status:      types.StatusDownloading,
		startTime:   time.Now(),
		messageID:   msgID,
		chatID:      chatID,
		userID:      userID,
		client:      GetAria2Client(),
		downloadDir: downloadDir,
	}
}

func (t *Aria2DownloadTask) GID() string { return t.gid }
func (t *Aria2DownloadTask) Name() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.name
}
func (t *Aria2DownloadTask) Size() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}
func (t *Aria2DownloadTask) DownloadedBytes() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.downloaded
}
func (t *Aria2DownloadTask) Speed() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.speed
}
func (t *Aria2DownloadTask) Progress() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.size <= 0 {
		return 0
	}
	p := (float64(t.downloaded) / float64(t.size)) * 100
	if p > 100 {
		return 100
	}
	return p
}
func (t *Aria2DownloadTask) ETA() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	remBytes := t.size - t.downloaded
	if remBytes <= 0 {
		return "0s"
	}
	sec := int64(float64(remBytes) / t.speed)
	return fmt.Sprintf("%ds", sec)
}
func (t *Aria2DownloadTask) Status() types.TaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}
func (t *Aria2DownloadTask) Engine() string       { return "Aria2c [MaxSpeed 16c]" }
func (t *Aria2DownloadTask) StartTime() time.Time { return t.startTime }
func (t *Aria2DownloadTask) MessageID() int       { return t.messageID }
func (t *Aria2DownloadTask) ChatID() int64        { return t.chatID }
func (t *Aria2DownloadTask) UserID() int64        { return t.userID }
func (t *Aria2DownloadTask) DownloadPath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.downloadPath
}

func (t *Aria2DownloadTask) Cancel() error {
	t.mu.Lock()
	t.status = types.StatusPaused
	t.mu.Unlock()
	return t.client.Cancel(t.gid)
}

func (t *Aria2DownloadTask) PollUntilComplete(ctx context.Context, onProgress func(cur, total int64)) (string, error) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = t.Cancel()
			return "", errors.New("download cancelled")
		case <-ticker.C:
			res, err := t.client.TellStatus(t.gid)
			if err != nil {
				continue
			}

			tot, _ := strconv.ParseInt(res.TotalLength, 10, 64)
			cur, _ := strconv.ParseInt(res.CompletedLength, 10, 64)
			spd, _ := strconv.ParseFloat(res.DownloadSpeed, 64)

			t.mu.Lock()
			t.size = tot
			t.downloaded = cur
			t.speed = spd

			if len(res.Files) > 0 {
				clean := filepath.Base(res.Files[0].Path)
				if clean != "" && clean != "." && clean != "/" {
					t.name = clean
				}
				t.downloadPath = res.Files[0].Path
			}
			t.mu.Unlock()

			if onProgress != nil {
				onProgress(cur, tot)
			}

			// If this download spawned a follow-up download (e.g. torrent metadata completed -> payload started)
			if len(res.FollowedBy) > 0 {
				newGID := res.FollowedBy[0]
				log.Printf("[Aria2c] Torrent followed: GID %s -> %s", t.gid, newGID)
				t.mu.Lock()
				t.gid = newGID
				t.mu.Unlock()
				continue
			}

			switch res.Status {
			case "complete":
				// If followedBy is present on completion, switch to payload
				if len(res.FollowedBy) > 0 {
					newGID := res.FollowedBy[0]
					log.Printf("[Aria2c] Torrent followed on complete: GID %s -> %s", t.gid, newGID)
					t.mu.Lock()
					t.gid = newGID
					t.mu.Unlock()
					continue
				}

				t.mu.Lock()
				t.status = types.StatusExtracting
				// If Bittorrent info name exists, determine if folder or single file
				if res.Bittorrent != nil && res.Bittorrent.Info.Name != "" {
					candidate := filepath.Join(res.Dir, res.Bittorrent.Info.Name)
					if _, err := os.Stat(candidate); err == nil {
						t.downloadPath = candidate
						t.name = res.Bittorrent.Info.Name
					}
				}
				if t.downloadPath == "" && len(res.Files) > 0 {
					t.downloadPath = res.Files[0].Path
				}
				path := t.downloadPath
				t.mu.Unlock()
				// Only sever peer connections & remove torrent to prevent background seeding for BitTorrent
				if res.Bittorrent != nil {
					_ = t.client.Cancel(t.gid)
				}
				return path, nil
			case "error":
				return "", fmt.Errorf("aria2 download failed: %s", res.ErrorMessage)
			case "removed":
				t.mu.RLock()
				path := t.downloadPath
				t.mu.RUnlock()
				if path != "" {
					if fi, sErr := os.Stat(path); sErr == nil && fi.Size() > 0 {
						return path, nil
					}
				}
				return "", errors.New("download cancelled or removed from aria2")
			}
		}
	}
}
