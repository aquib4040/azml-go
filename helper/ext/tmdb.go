package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
)

var (
	tmdbUrlRegex      = regexp.MustCompile(`(?i)https?://\S+`)
	tmdbTgLinkRegex   = regexp.MustCompile(`(?i)\b(?:t|telegram)\.me/\S+`)
	tmdbBracketRegex  = regexp.MustCompile(`[\[\](){}]`)
	tmdbExtRegex      = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|flv|webm|ts|m4v|wmv|iso|rar|zip|7z)$`)
	tmdbDomainRegex   = regexp.MustCompile(`(?i)\b(?:www\.)?\w+\.(?:com|net|org|xyz|me|in|to|co|cc|info|tv|link|app|online|site|club|work|icu|top|vip|pro)\b`)
	tmdbWwwRegex      = regexp.MustCompile(`(?i)\bwww\S*`)
	tmdbCleanDelim    = regexp.MustCompile(`[-_.]`)
	tmdbSpacesRegex   = regexp.MustCompile(`\s+`)
	tmdbYearRegex     = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	tmdbSeasonSxxExx  = regexp.MustCompile(`(?i)(?:^|[^\w])s0*(\d{1,2})e\d{1,2}(?:[^\w]|$)`)
	tmdbSeasonWord    = regexp.MustCompile(`(?i)\bseason\s*(\d{1,2})\b`)
	tmdbSeasonSimple  = regexp.MustCompile(`(?i)(?:^|[^\w])s0*(\d{1,2})(?:[^\w]|$)`)
	tmdbEpisodeRegex  = regexp.MustCompile(`(?i)(?:^|[^\w])(?:e\d{1,4}|ep\s*\d{1,4}|episode\s*\d{1,4})(?:[^\w]|$)`)
	tmdbTagStripRegex = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|4k|uhd|fhd|hdrip|web-dl|webrip|bluray|brrip|dvdrip|hdtv|remux|x264|x265|h264|h265|hevc|10bit|8bit|hdr|hdr10|dovi|dv|avc|aac|ac3|eac3|dts|ddp5\.1|atmos|mp3|flac|dual audio|multi audio|hindi|english|tamil|telugu)\b`)
)

// FormatCleanPosterTitle extracts clean movie/show title, season, and year for TMDB search
func FormatCleanPosterTitle(rawTitle, renameRegex string) (title, season, year string) {
	if unquoted, err := url.QueryUnescape(rawTitle); err == nil {
		rawTitle = unquoted
	}

	if renameRegex != "" {
		rawTitle = applyRegexRemname(rawTitle, renameRegex)
	}

	t := tmdbUrlRegex.ReplaceAllString(rawTitle, " ")
	t = tmdbTgLinkRegex.ReplaceAllString(t, " ")
	t = tmdbBracketRegex.ReplaceAllString(t, " ")
	t = tmdbExtRegex.ReplaceAllString(t, "")
	t = tmdbDomainRegex.ReplaceAllString(t, " ")
	t = tmdbWwwRegex.ReplaceAllString(t, " ")
	t = tmdbCleanDelim.ReplaceAllString(t, " ")
	t = tmdbTagStripRegex.ReplaceAllString(t, " ")
	t = tmdbSpacesRegex.ReplaceAllString(t, " ")
	t = strings.TrimSpace(t)

	// Extract season
	if match := tmdbSeasonSxxExx.FindStringSubmatchIndex(t); len(match) >= 4 {
		sNum := t[match[2]:match[3]]
		if n, err := strconv.Atoi(sNum); err == nil {
			season = fmt.Sprintf("Season %d", n)
		}
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
		return finalCleanTitle(t), season, ""
	}

	if match := tmdbSeasonWord.FindStringSubmatchIndex(t); len(match) >= 4 {
		sNum := t[match[2]:match[3]]
		if n, err := strconv.Atoi(sNum); err == nil {
			season = fmt.Sprintf("Season %d", n)
		}
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
		return finalCleanTitle(t), season, ""
	}

	if match := tmdbSeasonSimple.FindStringSubmatchIndex(t); len(match) >= 4 {
		sNum := t[match[2]:match[3]]
		if n, err := strconv.Atoi(sNum); err == nil {
			season = fmt.Sprintf("Season %d", n)
		}
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
		return finalCleanTitle(t), season, ""
	}

	// Remove episode numbers
	if match := tmdbEpisodeRegex.FindStringIndex(t); len(match) == 2 {
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
	}

	// Extract year
	allYears := tmdbYearRegex.FindAllStringIndex(t, -1)
	if len(allYears) > 0 {
		lastYearMatch := allYears[len(allYears)-1]
		year = t[lastYearMatch[0]:lastYearMatch[1]]
		if lastYearMatch[0] <= 1 {
			t = strings.TrimSpace(t[lastYearMatch[1]:])
		} else {
			t = strings.TrimSpace(t[:lastYearMatch[0]])
		}
	}

	return finalCleanTitle(t), season, year
}

func finalCleanTitle(t string) string {
	t = tmdbBracketRegex.ReplaceAllString(t, "")
	t = tmdbSpacesRegex.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

type tmdbSearchResult struct {
	Results []struct {
		ID           int    `json:"id"`
		MediaType    string `json:"media_type"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		BackdropPath string `json:"backdrop_path"`
		PosterPath   string `json:"poster_path"`
	} `json:"results"`
}

type tmdbImageItem struct {
	FilePath string  `json:"file_path"`
	ISO6391  *string `json:"iso_639_1"`
}

type tmdbImagesResult struct {
	Backdrops []tmdbImageItem `json:"backdrops"`
	Posters   []tmdbImageItem `json:"posters"`
}

// GetTMDBPosterLink queries TMDB API to find poster or landscape backdrop URL
func GetTMDBPosterLink(ctx context.Context, title, year string, asDoc bool) (string, error) {
	cfg := config.Get()
	token := strings.TrimSpace(cfg.TMDBAccessToken)
	if token == "" {
		return "", nil
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	searchURL := "https://api.themoviedb.org/3/search/multi"
	q := url.Values{}
	q.Set("query", title)
	q.Set("include_adult", "false")
	q.Set("language", "en-US")
	q.Set("page", "1")
	if year != "" {
		q.Set("year", year)
	}

	if len(token) < 50 {
		q.Set("api_key", token)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if len(token) >= 50 {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tmdb search returned status %d", resp.StatusCode)
	}

	var searchRes tmdbSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&searchRes); err != nil {
		return "", err
	}

	if len(searchRes.Results) == 0 {
		return "", nil
	}

	var bestItem *struct {
		ID           int    `json:"id"`
		MediaType    string `json:"media_type"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		BackdropPath string `json:"backdrop_path"`
		PosterPath   string `json:"poster_path"`
	}

	for i := range searchRes.Results {
		if searchRes.Results[i].MediaType != "person" {
			bestItem = &searchRes.Results[i]
			break
		}
	}

	if bestItem == nil {
		return "", nil
	}

	// Fetch image sets for media
	imgURL := fmt.Sprintf("https://api.themoviedb.org/3/%s/%d/images", bestItem.MediaType, bestItem.ID)
	iq := url.Values{}
	iq.Set("include_image_languages", "en,null")
	if len(token) < 50 {
		iq.Set("api_key", token)
	}

	imgReq, err := http.NewRequestWithContext(ctx, "GET", imgURL+"?"+iq.Encode(), nil)
	if err == nil {
		imgReq.Header.Set("Accept", "application/json")
		if len(token) >= 50 {
			imgReq.Header.Set("Authorization", "Bearer "+token)
		}
		if imgResp, err := httpClient.Do(imgReq); err == nil {
			defer imgResp.Body.Close()
			if imgResp.StatusCode == http.StatusOK {
				var imagesRes tmdbImagesResult
				if err := json.NewDecoder(imgResp.Body).Decode(&imagesRes); err == nil {
					imgPath := selectBestImagePath(imagesRes, asDoc)
					if imgPath != "" {
						return "https://image.tmdb.org/t/p/original" + imgPath, nil
					}
				}
			}
		}
	}

	// Fallback to basic search paths
	fallback := bestItem.BackdropPath
	if asDoc && bestItem.PosterPath != "" {
		fallback = bestItem.PosterPath
	} else if fallback == "" {
		fallback = bestItem.PosterPath
	}

	if fallback != "" {
		return "https://image.tmdb.org/t/p/original" + fallback, nil
	}

	return "", nil
}

func selectBestImagePath(res tmdbImagesResult, asDoc bool) string {
	var enBackdrops, nullBackdrops, allBackdrops []string
	for _, b := range res.Backdrops {
		if b.ISO6391 != nil && *b.ISO6391 == "en" {
			enBackdrops = append(enBackdrops, b.FilePath)
		} else if b.ISO6391 == nil {
			nullBackdrops = append(nullBackdrops, b.FilePath)
		}
		allBackdrops = append(allBackdrops, b.FilePath)
	}

	var enPosters, nullPosters, allPosters []string
	for _, p := range res.Posters {
		if p.ISO6391 != nil && *p.ISO6391 == "en" {
			enPosters = append(enPosters, p.FilePath)
		} else if p.ISO6391 == nil {
			nullPosters = append(nullPosters, p.FilePath)
		}
		allPosters = append(allPosters, p.FilePath)
	}

	if asDoc {
		// Document priorities: poster first
		if len(enPosters) > 0 {
			return enPosters[0]
		}
		if len(nullPosters) > 0 {
			return nullPosters[0]
		}
		if len(allPosters) > 0 {
			return allPosters[0]
		}
		if len(enBackdrops) > 0 {
			return enBackdrops[0]
		}
		if len(nullBackdrops) > 0 {
			return nullBackdrops[0]
		}
	} else {
		// Video priorities: 16:9 backdrop first for cover
		if len(enBackdrops) > 0 {
			return enBackdrops[0]
		}
		if len(nullBackdrops) > 0 {
			return nullBackdrops[0]
		}
		if len(allBackdrops) > 0 {
			return allBackdrops[0]
		}
		if len(enPosters) > 0 {
			return enPosters[0]
		}
		if len(allPosters) > 0 {
			return allPosters[0]
		}
	}

	return ""
}

// DownloadImage downloads an image URL directly to destPath
func DownloadImage(ctx context.Context, imageURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download image failed with status %d", resp.StatusCode)
	}

	_ = os.MkdirAll(filepath.Dir(destPath), 0755)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// GetAutoThumbnail searches TMDB for title and returns a local high-res poster file path
func GetAutoThumbnail(ctx context.Context, rawFilename string, asDoc bool, renameRegex string) (string, error) {
	title, _, year := FormatCleanPosterTitle(rawFilename, renameRegex)
	if len(title) < 2 {
		return "", nil
	}

	posterURL, err := GetTMDBPosterLink(ctx, title, year, asDoc)
	if err != nil || posterURL == "" {
		return "", err
	}

	destDir := "Thumbnails"
	_ = os.MkdirAll(destDir, 0755)
	destPath := filepath.Join(destDir, fmt.Sprintf("tmdb_%s.jpg", ShortHash(rawFilename)))

	if err := DownloadImage(ctx, posterURL, destPath); err != nil {
		return "", err
	}

	return destPath, nil
}
