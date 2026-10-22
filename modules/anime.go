package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type AnimeModule struct {
	sender *telegram.MessageSender
}

func NewAnimeModule(sender *telegram.MessageSender) *AnimeModule {
	return &AnimeModule{sender: sender}
}

const animeGraphQLQuery = `query ($search: String) {
  Media(search: $search, type: ANIME) {
    id
    title {
      romaji
      english
      native
    }
    format
    status
    episodes
    duration
    season
    seasonYear
    averageScore
    genres
    description(asHtml: false)
    siteUrl
    coverImage {
      large
    }
  }
}`

type anilistResponse struct {
	Data struct {
		Media struct {
			ID    int `json:"id"`
			Title struct {
				Romaji  string `json:"romaji"`
				English string `json:"english"`
				Native  string `json:"native"`
			} `json:"title"`
			Format       string   `json:"format"`
			Status       string   `json:"status"`
			Episodes     int      `json:"episodes"`
			Duration     int      `json:"duration"`
			Season       string   `json:"season"`
			SeasonYear   int      `json:"seasonYear"`
			AverageScore int      `json:"averageScore"`
			Genres       []string `json:"genres"`
			Description  string   `json:"description"`
			SiteURL      string   `json:"siteUrl"`
			CoverImage   struct {
				Large string `json:"large"`
			} `json:"coverImage"`
		} `json:"Media"`
	} `json:"data"`
}

func (m *AnimeModule) HandleAnime(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		helpMsg := "<b>Anime Search Usage:</b>\n" +
			"• <code>/anime [anime title]</code>\n" +
			"• <code>/anilist [anime title]</code>"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, nil)
		return
	}

	query := strings.Join(parts[1:], " ")
	waitID, _ := m.sender.SendMessage(ctx, peer, "<i>Searching AniList...</i>", msg.MessageID, nil)

	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		reqBody, _ := json.Marshal(map[string]interface{}{
			"query": animeGraphQLQuery,
			"variables": map[string]string{
				"search": query,
			},
		})

		req, err := http.NewRequestWithContext(ctx, "POST", "https://graphql.anilist.co", bytes.NewReader(reqBody))
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, "Error creating request", nil)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			_ = m.sender.EditMessage(ctx, peer, waitID, "<i>No Anime results found.</i>", nil)
			return
		}
		defer resp.Body.Close()

		var res anilistResponse
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || res.Data.Media.ID == 0 {
			_ = m.sender.EditMessage(ctx, peer, waitID, "<i>No Anime results found.</i>", nil)
			return
		}

		media := res.Data.Media
		title := media.Title.Romaji
		if media.Title.English != "" {
			title = fmt.Sprintf("%s (%s)", media.Title.Romaji, media.Title.English)
		}

		desc := cleanHTMLTags(media.Description)
		if len(desc) > 400 {
			desc = desc[:400] + "..."
		}

		genres := strings.Join(media.Genres, ", ")
		if genres == "" {
			genres = "N/A"
		}

		text := fmt.Sprintf("🌸 <b>%s</b>\n"+
			"⭐ <b>Score:</b> <code>%d%%</code>\n"+
			"📺 <b>Format:</b> <code>%s</code>\n"+
			"⚡ <b>Status:</b> <code>%s</code>\n"+
			"🎞 <b>Episodes:</b> <code>%d (%d min)</code>\n"+
			"📅 <b>Season:</b> <code>%s %d</code>\n"+
			"🎭 <b>Genres:</b> <code>%s</code>\n\n"+
			"📝 <b>Synopsis:</b>\n<i>%s</i>",
			title, media.AverageScore, media.Format, media.Status,
			media.Episodes, media.Duration, media.Season, media.SeasonYear,
			genres, desc)

		btn := telegram.NewButtonMaker()
		if media.SiteURL != "" {
			btn.UButton("🌸 Open on AniList", media.SiteURL)
		}

		_ = m.sender.EditMessage(ctx, peer, waitID, text, btn.BuildMenu(1))
	}()
}

func cleanHTMLTags(src string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	cleaned := re.ReplaceAllString(src, "")
	return strings.TrimSpace(cleaned)
}
