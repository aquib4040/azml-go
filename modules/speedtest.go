package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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

		cmd := exec.CommandContext(testCtx, "speedtest-cli", "--share", "--json")
		var out bytes.Buffer
		var errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut

		if err := cmd.Run(); err != nil {
			log.Printf("[AZML-Go Speedtest] Error running speedtest-cli: %v (stderr: %s)", err, errOut.String())
			_ = m.sender.EditMessage(ctx, peer, waitID, "<b>ERROR:</b> <i>Can't connect to Server at the Moment, Try Again Later !</i>", nil)
			return
		}

		var res speedtestResult
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			log.Printf("[AZML-Go Speedtest] Error parsing JSON output: %v", err)
			_ = m.sender.EditMessage(ctx, peer, waitID, "<b>ERROR:</b> <i>Can't connect to Server at the Moment, Try Again Later !</i>", nil)
			return
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
