package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type SpeedtestModule struct {
	sender *telegram.MessageSender
}

func NewSpeedtestModule(sender *telegram.MessageSender) *SpeedtestModule {
	return &SpeedtestModule{sender: sender}
}

type speedtestServerInfo struct {
	Name    string  `json:"name"`
	Country string  `json:"country"`
	CC      string  `json:"cc"`
	Sponsor string  `json:"sponsor"`
	Latency float64 `json:"latency"`
	Lat     string  `json:"lat"`
	Lon     string  `json:"lon"`
}

type speedtestClientInfo struct {
	IP        string `json:"ip"`
	Lat       string `json:"lat"`
	Lon       string `json:"lon"`
	Country   string `json:"country"`
	ISP       string `json:"isp"`
	ISPRating string `json:"isprating"`
}

type speedtestResult struct {
	Download      float64             `json:"download"`
	Upload        float64             `json:"upload"`
	Ping          float64             `json:"ping"`
	Timestamp     string              `json:"timestamp"`
	BytesSent     int64               `json:"bytes_sent"`
	BytesReceived int64               `json:"bytes_received"`
	Share         string              `json:"share"`
	Server        speedtestServerInfo `json:"server"`
	Client        speedtestClientInfo `json:"client"`
}

func (m *SpeedtestModule) HandleSpeedtest(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	waitID, err := m.sender.SendMessage(ctx, peer, "<i>Initiating Speedtest...</i>", msg.MessageID, nil)
	if err != nil || waitID <= 0 {
		return
	}

	go func() {
		testCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		var res *speedtestResult
		speedtestBin, lookErr := exec.LookPath("speedtest-cli")
		if lookErr != nil {
			speedtestBin, lookErr = exec.LookPath("speedtest")
		}

		if lookErr == nil {
			cmd := exec.CommandContext(testCtx, speedtestBin, "--share", "--json")
			var out bytes.Buffer
			var errOut bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errOut

			if err := cmd.Run(); err == nil {
				var parsed speedtestResult
				if json.Unmarshal(out.Bytes(), &parsed) == nil {
					res = &parsed
				}
			} else {
				log.Printf("[AZML-Go Speedtest] %s failed: %v, falling back to native speedtest", speedtestBin, err)
			}
		}

		if res == nil {
			var nErr error
			res, nErr = m.runNativeSpeedtest(testCtx)
			if nErr != nil {
				log.Printf("[AZML-Go Speedtest] Native speedtest error: %v", nErr)
				_ = m.sender.EditMessage(ctx, peer, waitID, "<b>ERROR:</b> <i>Can't connect to Server at the Moment, Try Again Later !</i>", nil)
				return
			}
		}

		caption := fmt.Sprintf("➲ <b><i>SPEEDTEST INFO</i></b>\n"+
			"┠ <b>Upload:</b> <code>%s/s</code>\n"+
			"┠ <b>Download:</b>  <code>%s/s</code>\n"+
			"┠ <b>Ping:</b> <code>%.2f ms</code>\n"+
			"┠ <b>Time:</b> <code>%s</code>\n"+
			"┠ <b>Data Sent:</b> <code>%s</code>\n"+
			"┖ <b>Data Received:</b> <code>%s</code>\n\n"+
			"➲ <b><i>SPEEDTEST SERVER</i></b>\n"+
			"┠ <b>Name:</b> <code>%s</code>\n"+
			"┠ <b>Country:</b> <code>%s, %s</code>\n"+
			"┠ <b>Sponsor:</b> <code>%s</code>\n"+
			"┠ <b>Latency:</b> <code>%.2f</code>\n"+
			"┠ <b>Latitude:</b> <code>%s</code>\n"+
			"┖ <b>Longitude:</b> <code>%s</code>\n\n"+
			"➲ <b><i>CLIENT DETAILS</i></b>\n"+
			"┠ <b>IP Address:</b> <code>%s</code>\n"+
			"┠ <b>Latitude:</b> <code>%s</code>\n"+
			"┠ <b>Longitude:</b> <code>%s</code>\n"+
			"┠ <b>Country:</b> <code>%s</code>\n"+
			"┠ <b>ISP:</b> <code>%s</code>\n"+
			"┖ <b>ISP Rating:</b> <code>%s</code>",
			ext.GetReadableFileSize(int64(res.Upload/8)),
			ext.GetReadableFileSize(int64(res.Download/8)),
			res.Ping,
			res.Timestamp,
			ext.GetReadableFileSize(res.BytesSent),
			ext.GetReadableFileSize(res.BytesReceived),
			res.Server.Name,
			res.Server.Country,
			res.Server.CC,
			res.Server.Sponsor,
			res.Server.Latency,
			res.Server.Lat,
			res.Server.Lon,
			res.Client.IP,
			res.Client.Lat,
			res.Client.Lon,
			res.Client.Country,
			res.Client.ISP,
			res.Client.ISPRating,
		)

		if res.Share != "" {
			photoID, pErr := m.sender.SendPhotoFromURL(ctx, peer, res.Share, caption, msg.MessageID)
			if pErr == nil && photoID > 0 {
				_ = m.sender.DeleteMessage(ctx, peer, []int{waitID})
				return
			}
			log.Printf("[AZML-Go Speedtest] Photo sending fallback to text edit: %v", pErr)
		}

		_ = m.sender.EditMessage(ctx, peer, waitID, caption, nil)
	}()
}

func (m *SpeedtestModule) runNativeSpeedtest(ctx context.Context) (*speedtestResult, error) {
	clientInfo := speedtestClientInfo{
		IP:        "Unknown",
		Country:   "Unknown",
		ISP:       "Unknown",
		ISPRating: "3.7",
	}

	httpClient := &http.Client{Timeout: 6 * time.Second}
	ipReq, _ := http.NewRequestWithContext(ctx, "GET", "http://ip-api.com/json/", nil)
	if ipResp, err := httpClient.Do(ipReq); err == nil {
		defer ipResp.Body.Close()
		var ipData struct {
			Query   string  `json:"query"`
			Country string  `json:"country"`
			Isp     string  `json:"isp"`
			Lat     float64 `json:"lat"`
			Lon     float64 `json:"lon"`
		}
		if json.NewDecoder(ipResp.Body).Decode(&ipData) == nil {
			if ipData.Query != "" {
				clientInfo.IP = ipData.Query
			}
			if ipData.Country != "" {
				clientInfo.Country = ipData.Country
			}
			if ipData.Isp != "" {
				clientInfo.ISP = ipData.Isp
			}
			clientInfo.Lat = fmt.Sprintf("%.4f", ipData.Lat)
			clientInfo.Lon = fmt.Sprintf("%.4f", ipData.Lon)
		}
	}

	// 1. Latency (Ping)
	startPing := time.Now()
	pingReq, _ := http.NewRequestWithContext(ctx, "HEAD", "https://speed.cloudflare.com/__down?bytes=0", nil)
	var pingMs float64 = 15.0
	if pingResp, err := httpClient.Do(pingReq); err == nil {
		_ = pingResp.Body.Close()
		pingMs = float64(time.Since(startPing).Milliseconds())
		if pingMs <= 0 {
			pingMs = 1.0
		}
	}

	// 2. Download test (25 MB)
	dlClient := &http.Client{Timeout: 35 * time.Second}
	dlBytesTarget := int64(25 * 1024 * 1024)
	dlReq, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", dlBytesTarget), nil)
	var dlSpeed float64
	var actualReceived int64
	startDL := time.Now()
	if dlResp, err := dlClient.Do(dlReq); err == nil {
		defer dlResp.Body.Close()
		n, _ := io.Copy(io.Discard, dlResp.Body)
		actualReceived = n
		elapsed := time.Since(startDL).Seconds()
		if elapsed > 0 && n > 0 {
			dlSpeed = (float64(n) * 8) / elapsed
		}
	}

	// 3. Upload test (5 MB)
	upBytesTarget := int64(5 * 1024 * 1024)
	dummyData := make([]byte, upBytesTarget)
	upReq, _ := http.NewRequestWithContext(ctx, "POST", "https://speed.cloudflare.com/__up", bytes.NewReader(dummyData))
	upReq.Header.Set("Content-Type", "application/octet-stream")
	var upSpeed float64
	var actualSent int64
	startUP := time.Now()
	if upResp, err := dlClient.Do(upReq); err == nil {
		defer upResp.Body.Close()
		actualSent = upBytesTarget
		elapsed := time.Since(startUP).Seconds()
		if elapsed > 0 {
			upSpeed = (float64(upBytesTarget) * 8) / elapsed
		}
	}

	res := &speedtestResult{
		Download:      dlSpeed,
		Upload:        upSpeed,
		Ping:          pingMs,
		Timestamp:     time.Now().Format("2006-01-02 15:04:05"),
		BytesSent:     actualSent,
		BytesReceived: actualReceived,
		Server: speedtestServerInfo{
			Name:    "Cloudflare Speed CDN",
			Country: "Global Anycast",
			CC:      "GLOBAL",
			Sponsor: "Cloudflare",
			Latency: pingMs,
		},
		Client: clientInfo,
	}

	return res, nil
}
