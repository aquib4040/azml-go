package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type cliSpeedResult struct {
	Download float64 `json:"download"`
	Upload   float64 `json:"upload"`
	Ping     float64 `json:"ping"`
	Server   struct {
		Name    string  `json:"name"`
		Country string  `json:"country"`
		Latency float64 `json:"latency"`
	} `json:"server"`
	Client struct {
		IP  string `json:"ip"`
		ISP string `json:"isp"`
	} `json:"client"`
	BytesSent     int64 `json:"bytes_sent"`
	BytesReceived int64 `json:"bytes_received"`
}

func (m *SpeedtestModule) HandleSpeedtest(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	waitID, _ := m.sender.SendMessage(ctx, peer, "<i>Initiating Speedtest...</i>", msg.MessageID, nil)

	go func() {
		// 1. Try speedtest-cli command if installed
		if _, err := exec.LookPath("speedtest-cli"); err == nil {
			cmd := exec.CommandContext(ctx, "speedtest-cli", "--json")
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err == nil {
				var res cliSpeedResult
				if err := json.Unmarshal(out.Bytes(), &res); err == nil {
					text := fmt.Sprintf("➲ <b><i>SPEEDTEST INFO</i></b>\n"+
						"┠ <b>Upload:</b> <code>%s/s</code>\n"+
						"┠ <b>Download:</b> <code>%s/s</code>\n"+
						"┠ <b>Ping:</b> <code>%.2f ms</code>\n"+
						"┠ <b>Data Sent:</b> <code>%s</code>\n"+
						"┖ <b>Data Received:</b> <code>%s</code>\n\n"+
						"➲ <b><i>SPEEDTEST SERVER</i></b>\n"+
						"┠ <b>Name:</b> <code>%s</code>\n"+
						"┖ <b>Country:</b> <code>%s</code>\n\n"+
						"➲ <b><i>CLIENT DETAILS</i></b>\n"+
						"┠ <b>IP Address:</b> <code>%s</code>\n"+
						"┖ <b>ISP:</b> <code>%s</code>",
						ext.GetReadableFileSize(int64(res.Upload/8)),
						ext.GetReadableFileSize(int64(res.Download/8)),
						res.Ping,
						ext.GetReadableFileSize(res.BytesSent),
						ext.GetReadableFileSize(res.BytesReceived),
						res.Server.Name,
						res.Server.Country,
						res.Client.IP,
						res.Client.ISP,
					)
					_ = m.sender.EditMessage(ctx, peer, waitID, text, nil)
					return
				}
			}
		}

		// 2. Fallback to direct HTTP network bandwidth test
		client := &http.Client{Timeout: 20 * time.Second}

		// Ping test
		pingStart := time.Now()
		req, _ := http.NewRequestWithContext(ctx, "HEAD", "https://1.1.1.1", nil)
		resp, pErr := client.Do(req)
		pingMs := float64(time.Since(pingStart).Milliseconds())
		if pErr == nil {
			_ = resp.Body.Close()
		}

		// Download test (15 MB test payload)
		dlStart := time.Now()
		dlReq, _ := http.NewRequestWithContext(ctx, "GET", "https://speed.cloudflare.com/__down?bytes=15000000", nil)
		dlResp, dlErr := client.Do(dlReq)
		var dlBytes int64
		if dlErr == nil {
			dlBytes, _ = io.Copy(io.Discard, dlResp.Body)
			_ = dlResp.Body.Close()
		}
		dlDuration := time.Since(dlStart).Seconds()
		dlSpeed := float64(0)
		if dlDuration > 0 {
			dlSpeed = float64(dlBytes) / dlDuration
		}

		// Upload test (5 MB test payload)
		dummyData := make([]byte, 5*1024*1024)
		upStart := time.Now()
		upReq, _ := http.NewRequestWithContext(ctx, "POST", "https://speed.cloudflare.com/__up", bytes.NewReader(dummyData))
		upResp, upErr := client.Do(upReq)
		if upErr == nil {
			_, _ = io.Copy(io.Discard, upResp.Body)
			_ = upResp.Body.Close()
		}
		upDuration := time.Since(upStart).Seconds()
		upSpeed := float64(0)
		if upDuration > 0 {
			upSpeed = float64(len(dummyData)) / upDuration
		}

		text := fmt.Sprintf("➲ <b><i>SPEEDTEST INFO</i></b>\n"+
			"┠ <b>Upload:</b> <code>%s/s</code>\n"+
			"┠ <b>Download:</b> <code>%s/s</code>\n"+
			"┠ <b>Ping:</b> <code>%.2f ms</code>\n"+
			"┠ <b>Data Sent:</b> <code>%s</code>\n"+
			"┖ <b>Data Received:</b> <code>%s</code>\n\n"+
			"➲ <b><i>SPEEDTEST SERVER</i></b>\n"+
			"┠ <b>Provider:</b> <code>Cloudflare Speed Edge</code>\n"+
			"┖ <b>Method:</b> <code>Direct HTTP Probe</code>",
			ext.GetReadableFileSize(int64(upSpeed)),
			ext.GetReadableFileSize(int64(dlSpeed)),
			pingMs,
			ext.GetReadableFileSize(int64(len(dummyData))),
			ext.GetReadableFileSize(dlBytes),
		)
		_ = m.sender.EditMessage(ctx, peer, waitID, text, nil)
	}()
}
