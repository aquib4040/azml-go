package ext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type StreamInfo struct {
	Index     int               `json:"index"`
	CodecType string            `json:"codec_type"`
	CodecName string            `json:"codec_name"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Duration  string            `json:"duration"`
	Tags      map[string]string `json:"tags"`
}

func parseDurationString(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return int(f)
	}
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		secParts := strings.Split(parts[2], ".")
		sec, _ := strconv.Atoi(secParts[0])
		return h*3600 + m*60 + sec
	} else if len(parts) == 2 {
		m, _ := strconv.Atoi(parts[0])
		secParts := strings.Split(parts[1], ".")
		sec, _ := strconv.Atoi(secParts[0])
		return m*60 + sec
	}
	return 0
}

type FormatInfo struct {
	Duration string            `json:"duration"`
	BitRate  string            `json:"bit_rate"`
	Format   string            `json:"format_name"`
	Tags     map[string]string `json:"tags"`
}

type FFProbeOutput struct {
	Streams []StreamInfo `json:"streams"`
	Format  FormatInfo   `json:"format"`
}

type MediaDetails struct {
	Duration       int
	Width          int
	Height         int
	VideoCodec     string
	AudioCodec     string
	AudioLanguages []string
	SubLanguages   []string
	Quality        string
	Title          string
	Artist         string
}

func ProbeMedia(filePath string) (*MediaDetails, error) {
	cmd := exec.Command("ffprobe",
		"-hide_banner",
		"-loglevel", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var output FFProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return nil, err
	}

	details := &MediaDetails{}
	details.Duration = parseDurationString(output.Format.Duration)

	details.Title = output.Format.Tags["title"]
	details.Artist = output.Format.Tags["artist"]

	for _, s := range output.Streams {
		switch s.CodecType {
		case "video":
			if details.Duration == 0 {
				if d := parseDurationString(s.Duration); d > 0 {
					details.Duration = d
				} else if d := parseDurationString(s.Tags["DURATION"]); d > 0 {
					details.Duration = d
				} else if d := parseDurationString(s.Tags["DURATION-eng"]); d > 0 {
					details.Duration = d
				}
			}
			if details.Width == 0 {
				details.Width = s.Width
				details.Height = s.Height
				details.VideoCodec = s.CodecName
				if s.Height <= 480 {
					details.Quality = "480p"
				} else if s.Height <= 540 {
					details.Quality = "540p"
				} else if s.Height <= 720 {
					details.Quality = "720p"
				} else if s.Height <= 1080 {
					details.Quality = "1080p"
				} else if s.Height <= 2160 {
					details.Quality = "4K"
				} else {
					details.Quality = fmt.Sprintf("%dp", s.Height)
				}
			}
		case "audio":
			if details.AudioCodec == "" {
				details.AudioCodec = s.CodecName
			}
			if lang := s.Tags["language"]; lang != "" && lang != "und" {
				details.AudioLanguages = append(details.AudioLanguages, lang)
			}
		case "subtitle":
			if lang := s.Tags["language"]; lang != "" && lang != "und" {
				details.SubLanguages = append(details.SubLanguages, lang)
			}
		}
	}

	return details, nil
}

func EditMetadata(inputFile, outputFile string, meta map[string]string) error {
	allVal := meta["all"]
	getVal := func(key string) string {
		if v, ok := meta[key]; ok && v != "" {
			return v
		}
		return allVal
	}

	title := getVal("title")
	description := getVal("description")
	artist := getVal("artist")
	album := getVal("album")
	year := getVal("year")
	audio := getVal("audio")
	subtitle := getVal("subtitle")
	site := getVal("site")

	outExt := strings.ToLower(filepath.Ext(outputFile))
	isMP4 := outExt == ".mp4" || outExt == ".m4v"

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-ignore_unknown",
		"-i", inputFile,
	}

	var metaArgs []string

	titleTags := []string{"title", "TITLE", "Title"}
	if title != "" {
		for _, tag := range titleTags {
			metaArgs = append(metaArgs, "-metadata", fmt.Sprintf("%s=%s", tag, title))
		}
		metaArgs = append(metaArgs, "-metadata:s:v", fmt.Sprintf("title=%s", title))
	}

	descTags := []string{"description", "comment"}
	if description != "" {
		for _, tag := range descTags {
			metaArgs = append(metaArgs, "-metadata", fmt.Sprintf("%s=%s", tag, description))
		}
	}

	artistTags := []string{"artist", "author", "encoded_by"}
	if artist != "" {
		for _, tag := range artistTags {
			metaArgs = append(metaArgs, "-metadata", fmt.Sprintf("%s=%s", tag, artist))
		}
	}

	if album != "" {
		metaArgs = append(metaArgs, "-metadata", fmt.Sprintf("album=%s", album))
	}

	if year != "" {
		metaArgs = append(metaArgs, "-metadata", fmt.Sprintf("date=%s", year))
	}

	if site != "" {
		metaArgs = append(metaArgs, "-metadata", fmt.Sprintf("official_site=%s", site))
	}

	if audio != "" {
		metaArgs = append(metaArgs, "-metadata:s:a", fmt.Sprintf("title=%s", audio))
	}

	if subtitle != "" {
		metaArgs = append(metaArgs, "-metadata:s:s", fmt.Sprintf("title=%s", subtitle))
	}

	args = append(args, metaArgs...)

	if isMP4 {
		// MP4 container: stream copy video and audio, faststart moov atom at beginning for streaming
		args = append(args,
			"-map", "0:v:0?",
			"-map", "0:a?",
			"-c:v", "copy",
			"-c:a", "copy",
			"-movflags", "+faststart",
		)
	} else {
		// Matroska/MKV: preserve all video, audio, subtitles, and attachments
		args = append(args,
			"-map", "0:v:0?",
			"-map", "0:a?",
			"-map", "0:s?",
			"-map", "0:t?",
			"-c", "copy",
		)
	}

	args = append(args, outputFile, "-y")

	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(outputFile)
		return fmt.Errorf("ffmpeg metadata failed: %v, stderr: %s", err, stderr.String())
	}
	return nil
}

func ExtractStreams(inputFile, outDir string, streamOptions []string) ([]string, error) {
	_ = os.MkdirAll(outDir, 0755)
	details, err := ProbeMedia(inputFile)
	if err != nil {
		return nil, err
	}

	var outputs []string
	baseName := strings.TrimSuffix(filepath.Base(inputFile), filepath.Ext(inputFile))

	for _, opt := range streamOptions {
		switch strings.ToLower(opt) {
		case "video":
			outPath := filepath.Join(outDir, fmt.Sprintf("%s_video.mkv", baseName))
			cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", inputFile, "-map", "0:v:0", "-c:v", "copy", outPath, "-y")
			if cmd.Run() == nil {
				outputs = append(outputs, outPath)
			}
		case "audio":
			codec := details.AudioCodec
			ext := "mka"
			if codec == "aac" || codec == "mp3" || codec == "flac" {
				ext = codec
			}
			outPath := filepath.Join(outDir, fmt.Sprintf("%s_audio.%s", baseName, ext))
			cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", inputFile, "-map", "0:a:0", "-c:a", "copy", outPath, "-y")
			if cmd.Run() == nil {
				outputs = append(outputs, outPath)
			}
		case "subtitle":
			outPath := filepath.Join(outDir, fmt.Sprintf("%s_sub.srt", baseName))
			cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", inputFile, "-map", "0:s:0", "-c:s", "srt", outPath, "-y")
			if cmd.Run() == nil {
				outputs = append(outputs, outPath)
			}
		}
	}

	if len(outputs) == 0 {
		return nil, errors.New("no streams extracted")
	}
	return outputs, nil
}

// CheckFFmpegCommandSafety validates FFmpeg arguments when video/audio re-encoding is disabled
func CheckFFmpegCommandSafety(cmdParts []string) (bool, string) {
	blockedFlags := map[string]bool{
		"-vf": true, "-filter:v": true, "-af": true, "-filter:a": true,
		"-filter_complex": true, "-lavfi": true, "-filter": true, "-fpre": true,
		"-crf": true, "-b:v": true, "-b:a": true, "-b": true, "-bitrate": true,
		"-maxrate": true, "-minrate": true, "-bufsize": true,
		"-qscale": true, "-q:v": true, "-q:a": true, "-q": true,
		"-preset": true, "-tune": true, "-profile:v": true, "-profile:a": true,
		"-level": true, "-pix_fmt": true, "-r": true, "-framerate": true,
		"-fpsmax": true, "-s": true, "-video_size": true, "-aspect": true,
		"-ar": true, "-ac": true, "-ab": true,
	}

	for i, arg := range cmdParts {
		argLower := strings.ToLower(arg)
		if blockedFlags[argLower] {
			return false, fmt.Sprintf("Flag '%s' requires re-encoding which is disabled in Bot Settings", arg)
		}
		for prefix := range map[string]bool{"-filter": true, "-b": true, "-q": true, "-profile": true} {
			if strings.HasPrefix(argLower, prefix+":") {
				return false, fmt.Sprintf("Flag '%s' requires re-encoding which is disabled in Bot Settings", arg)
			}
		}

		if argLower == "-c" || argLower == "-codec" || argLower == "-vcodec" || argLower == "-acodec" ||
			argLower == "-scodec" || argLower == "-c:v" || argLower == "-c:a" || argLower == "-c:s" ||
			strings.HasPrefix(argLower, "-c:") || strings.HasPrefix(argLower, "-codec:") {
			if i+1 < len(cmdParts) {
				val := strings.ToLower(cmdParts[i+1])
				if val != "copy" && val != "none" {
					return false, fmt.Sprintf("Codec '%s' requires re-encoding. Only 'copy' or stream removal is allowed", val)
				}
			} else {
				return false, fmt.Sprintf("Missing codec value after '%s'", arg)
			}
		}
	}

	hasGlobalCopy := false
	for i := 0; i < len(cmdParts)-1; i++ {
		lower := strings.ToLower(cmdParts[i])
		if (lower == "-c" || lower == "-codec") && strings.ToLower(cmdParts[i+1]) == "copy" {
			hasGlobalCopy = true
			break
		}
	}

	hasVideoCopy := false
	hasAudioCopy := false
	for i, a := range cmdParts {
		l := strings.ToLower(a)
		if l == "-vn" || ((l == "-c:v" || l == "-vcodec" || strings.HasPrefix(l, "-c:v:")) && i+1 < len(cmdParts) && (strings.ToLower(cmdParts[i+1]) == "copy" || strings.ToLower(cmdParts[i+1]) == "none")) {
			hasVideoCopy = true
		}
		if l == "-an" || ((l == "-c:a" || l == "-acodec" || strings.HasPrefix(l, "-c:a:")) && i+1 < len(cmdParts) && (strings.ToLower(cmdParts[i+1]) == "copy" || strings.ToLower(cmdParts[i+1]) == "none")) {
			hasAudioCopy = true
		}
	}

	if !hasGlobalCopy && !(hasVideoCopy && hasAudioCopy) {
		return false, "FFmpeg command must specify '-c copy' or explicit '-c:v copy' / '-c:a copy' to prevent default re-encoding."
	}

	return true, ""
}

// ExecuteCustomFFmpeg runs user-specified FFmpeg arguments with safety enforcement
func ExecuteCustomFFmpeg(inputFile, outputFile string, rawCmd string, allowEncoding bool) error {
	parts := strings.Fields(rawCmd)
	if len(parts) == 0 {
		return errors.New("empty ffmpeg command")
	}

	if !allowEncoding {
		safe, reason := CheckFFmpegCommandSafety(parts)
		if !safe {
			return fmt.Errorf("ffmpeg command blocked: %s", reason)
		}
	}

	args := []string{"-hide_banner", "-loglevel", "error", "-i", inputFile}
	args = append(args, parts...)
	args = append(args, outputFile, "-y")

	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg execution failed: %v, stderr: %s", err, stderr.String())
	}
	return nil
}
