package downloader

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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

type Aria2StatusResponse struct {
	GID             string      `json:"gid"`
	Status          string      `json:"status"` // "active", "waiting", "paused", "error", "complete", "removed"
	TotalLength     string      `json:"totalLength"`
	CompletedLength string      `json:"completedLength"`
	UploadSpeed     string      `json:"uploadSpeed"`
	DownloadSpeed   string      `json:"downloadSpeed"`
	ErrorMessage    string      `json:"errorMessage"`
	Dir             string      `json:"dir"`
	Files           []Aria2File `json:"files"`
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

func GetAria2Client() *Aria2RPCClient {
	aria2Once.Do(func() {
		Aria2Client = &Aria2RPCClient{
			rpcURL: "http://127.0.0.1:6800/jsonrpc",
			httpClient: &http.Client{
				Timeout: 10 * time.Second,
			},
		}
		_ = Aria2Client.EnsureStarted()
	})
	return Aria2Client
}

func (a *Aria2RPCClient) EnsureStarted() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if already running on port 6800
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "ping",
		Method:  "aria2.getVersion",
		Params:  []interface{}{},
	}
	if _, err := a.call(req); err == nil {
		a.started = true
		return nil
	}

	// Start aria2c / studymaterial daemon with max connection and speed optimizations
	binName := "aria2c"
	if _, err := exec.LookPath("studymaterial"); err == nil {
		binName = "studymaterial"
	} else if _, err := os.Stat("/usr/bin/studymaterial"); err == nil {
		binName = "/usr/bin/studymaterial"
	}
	cmd := exec.Command(binName,
		"--enable-rpc",
		"--rpc-listen-all=true",
		"--rpc-listen-port=6800",
		"--max-connection-per-server=16",
		"--split=16",
		"--min-split-size=1M",
		"--max-concurrent-downloads=10",
		"--piece-length=1M",
		"--allow-overwrite=true",
		"--auto-file-renaming=false",
		"--summary-interval=1",
		"--disk-cache=64M",
		"--bt-max-peers=128",
		"--bt-request-peer-speed-limit=0",
		"--seed-time=0",
		"--seed-ratio=0.0",
		"--check-certificate=false",
		"--bt-require-crypto=true",
		"--bt-min-crypto-level=arc4",
		"--enable-dht=true",
		"--daemon=true",
	)

	_ = cmd.Run()
	time.Sleep(1 * time.Second)
	a.started = true
	return nil
}

func (a *Aria2RPCClient) call(req JSONRPCRequest) (*JSONRPCResponse, error) {
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := a.httpClient.Post(a.rpcURL, "application/json", bytes.NewReader(reqData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("aria2 error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return &rpcResp, nil
}

func (a *Aria2RPCClient) AddURI(link, downloadDir, filename string, headers []string) (string, error) {
	_ = os.MkdirAll(downloadDir, 0755)

	opts := map[string]interface{}{
		"dir":                        downloadDir,
		"max-connection-per-server":  "16",
		"split":                      "16",
		"min-split-size":             "1M",
		"allow-overwrite":            "true",
		"check-certificate":          "false",
		"bt-max-peers":               "128",
		"bt-request-peer-speed-limit": "0",
		"seed-time":                  "0",
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
		"dir":                        downloadDir,
		"max-connection-per-server":  "16",
		"split":                      "16",
		"min-split-size":             "1M",
		"bt-max-peers":               "128",
		"bt-request-peer-speed-limit": "0",
		"seed-time":                  "0",
		"seed-ratio":                 "0.0",
		"allow-overwrite":            "true",
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
	return &Aria2DownloadTask{
		gid:         gid,
		name:        name,
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
				if t.name == "" || t.name == "Unknown" {
					t.name = res.Files[0].Path
				}
				t.downloadPath = res.Files[0].Path
			}
			t.mu.Unlock()

			if onProgress != nil {
				onProgress(cur, tot)
			}

			switch res.Status {
			case "complete":
				t.mu.Lock()
				t.status = types.StatusExtracting
				path := t.downloadPath
				t.mu.Unlock()
				// Immediately sever all peer connections & remove torrent to prevent any seeding
				_ = t.client.Cancel(t.gid)
				return path, nil
			case "error":
				return "", fmt.Errorf("aria2 download failed: %s", res.ErrorMessage)
			case "removed":
				return "", errors.New("download cancelled or removed from aria2")
			}
		}
	}
}

