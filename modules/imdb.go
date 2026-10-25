package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type ImdbModule struct {
	sender *telegram.MessageSender
}

func NewImdbModule(sender *telegram.MessageSender) *ImdbModule {
	return &ImdbModule{sender: sender}
}

type tmdbMovieItem struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
}

type tmdbSearchMulti struct {
	Results []tmdbMovieItem `json:"results"`
}

func (m *ImdbModule) HandleImdb(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		helpMsg := "<b>IMDb Search Usage:</b>\n" +
			"• <code>/imdb [movie or series name]</code>\n" +
			"• <code>/imdb tt1234567</code>"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, nil)
		return
	}

	query := strings.Join(parts[1:], " ")
	waitID, _ := m.sender.SendMessage(ctx, peer, "<i>Searching IMDb...</i>", msg.MessageID, nil)

	go func() {
		cfg := config.Get()
		token := cfg.TMDBAccessToken
		httpClient := &http.Client{Timeout: 10 * time.Second}

		searchURL := fmt.Sprintf("https://api.themoviedb.org/3/search/multi?query=%s&include_adult=false&language=en-US&page=1", url.QueryEscape(query))
		if len(token) < 50 && token != "" {
			searchURL += "&api_key=" + token
		}

		req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, "Error building request", nil)
			return
		}
		if len(token) >= 50 {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := httpClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			// Free fallback endpoint
			fbURL := fmt.Sprintf("https://imdb.iamidiotareyoutoo.com/search?q=%s", url.QueryEscape(query))
			if fbResp, fbErr := httpClient.Get(fbURL); fbErr == nil {
				defer fbResp.Body.Close()
				var fbData struct {
					Description []struct {
						Title   string `json:"#TITLE"`
						Year    int    `json:"#YEAR"`
						Actors  string `json:"#ACTORS"`
						IMDbID  string `json:"#IMDB_ID"`
						IMDbURL string `json:"#IMDB_URL"`
						Img     string `json:"#IMG_POSTER"`
					} `json:"description"`
				}
				if json.NewDecoder(fbResp.Body).Decode(&fbData) == nil && len(fbData.Description) > 0 {
					item := fbData.Description[0]
					text := fmt.Sprintf("⌬ <b><u>%s (%d)</u></b>\n"+
						"• <b>Actors:</b> <code>%s</code>\n"+
						"• <b>IMDb ID:</b> <code>%s</code>",
						item.Title, item.Year, item.Actors, item.IMDbID)

					btn := telegram.NewButtonMaker()
					if item.IMDbURL != "" {
						btn.UButtonStyled("◈ "+telegram.ToSmallCaps("Open on IMDb"), item.IMDbURL, "primary")
					}
					_ = m.sender.EditMessageWithButtons(ctx, peer, waitID, text, btn, 1)
					return
				}
			}
			_ = m.sender.EditMessage(ctx, peer, waitID, "<i>No IMDb results found.</i>", nil)
			return
		}
		defer resp.Body.Close()

		var res tmdbSearchMulti
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || len(res.Results) == 0 {
			_ = m.sender.EditMessage(ctx, peer, waitID, "<i>No IMDb results found.</i>", nil)
			return
		}

		item := res.Results[0]
		name := item.Title
		if name == "" {
			name = item.Name
		}
		date := item.ReleaseDate
		if date == "" {
			date = item.FirstAirDate
		}
		year := date
		if len(year) >= 4 {
			year = year[:4]
		}

		overview := item.Overview
		if len(overview) > 500 {
			overview = overview[:500] + "..."
		}

		text := fmt.Sprintf("⌬ <b><u>%s (%s)</u></b>\n"+
			"• <b>Rating:</b> <code>%.1f / 10</code>\n"+
			"• <b>Release:</b> <code>%s</code>\n\n"+
			"• <b>Overview:</b>\n<i>%s</i>",
			name, year, item.VoteAverage, date, overview)

		btn := telegram.NewButtonMaker()
		btn.UButtonStyled("◈ "+telegram.ToSmallCaps("TMDb Page"), fmt.Sprintf("https://www.themoviedb.org/%s/%d", "movie", item.ID), "primary")
		_ = m.sender.EditMessageWithButtons(ctx, peer, waitID, text, btn, 1)
	}()
}
