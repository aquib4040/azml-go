package ext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetReadableFileSize(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected string
	}{
		{"zero bytes", 0, "0 B"},
		{"negative bytes", -10, "0 B"},
		{"small bytes", 500, "500 B"},
		{"exact 1 KB", 1024, "1.00 KB"},
		{"1.5 KB", 1536, "1.50 KB"},
		{"1 MB", 1024 * 1024, "1.00 MB"},
		{"1 GB", 1024 * 1024 * 1024, "1.00 GB"},
		{"1 TB", 1024 * 1024 * 1024 * 1024, "1.00 TB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetReadableFileSize(tt.input)
			if result != tt.expected {
				t.Errorf("GetReadableFileSize(%d) = %q; want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetReadableTime(t *testing.T) {
	tests := []struct {
		name     string
		seconds  int64
		expected string
	}{
		{"zero seconds", 0, "0s"},
		{"negative seconds", -5, "0s"},
		{"30 seconds", 30, "30s"},
		{"60 seconds", 60, "1m"},
		{"3600 seconds", 3600, "1h"},
		{"3665 seconds", 3665, "1h 1m 5s"},
		{"86400 seconds", 86400, "1d"},
		{"90061 seconds", 90061, "1d 1h 1m 1s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetReadableTime(tt.seconds)
			if result != tt.expected {
				t.Errorf("GetReadableTime(%d) = %q; want %q", tt.seconds, result, tt.expected)
			}
		})
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"http://example.com", true},
		{"https://example.com/file.zip", true},
		{"ftp://example.com", false},
		{"invalid-url", false},
		{"", false},
	}

	for _, tt := range tests {
		result := IsURL(tt.url)
		if result != tt.expected {
			t.Errorf("IsURL(%q) = %v; want %v", tt.url, result, tt.expected)
		}
	}
}

func TestIsMagnet(t *testing.T) {
	tests := []struct {
		link     string
		expected bool
	}{
		{"magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709&dn=test", true},
		{"MAGNET:?XT=URN:BTIH:DA39A3EE5E6B4B0D3255BFEF95601890AFD80709", true},
		{"https://example.com", false},
		{"magnet:?notxt=something", false},
	}

	for _, tt := range tests {
		result := IsMagnet(tt.link)
		if result != tt.expected {
			t.Errorf("IsMagnet(%q) = %v; want %v", tt.link, result, tt.expected)
		}
	}
}

func TestIsMegaLink(t *testing.T) {
	tests := []struct {
		link     string
		expected bool
	}{
		{"https://mega.nz/file/ABCDE123#secretkey", true},
		{"https://mega.nz/folder/XYZ987#folderkey", true},
		{"https://google.com", false},
	}

	for _, tt := range tests {
		result := IsMegaLink(tt.link)
		if result != tt.expected {
			t.Errorf("IsMegaLink(%q) = %v; want %v", tt.link, result, tt.expected)
		}
	}
}

func TestIsTelegramLink(t *testing.T) {
	tests := []struct {
		link     string
		expected bool
	}{
		{"https://t.me/somechannel/123", true},
		{"https://t.me/c/123456789/42", true},
		{"https://telegram.org", false},
	}

	for _, tt := range tests {
		result := IsTelegramLink(tt.link)
		if result != tt.expected {
			t.Errorf("IsTelegramLink(%q) = %v; want %v", tt.link, result, tt.expected)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	input := `folder/file:name*with?bad"chars<and>pipe|.txt`
	result := SanitizeFilename(input)
	forbidden := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range forbidden {
		if strings.Contains(result, char) {
			t.Errorf("SanitizeFilename result %q still contains forbidden character %q", result, char)
		}
	}
}

func TestRandomHex(t *testing.T) {
	for _, byteLen := range []int{4, 8, 16} {
		hexStr := RandomHex(byteLen)
		if len(hexStr) != byteLen*2 {
			t.Errorf("RandomHex(%d) produced length %d; want %d", byteLen, len(hexStr), byteLen*2)
		}
	}
}

func TestShortHash(t *testing.T) {
	h1 := ShortHash("hello world")
	h2 := ShortHash("hello world")
	h3 := ShortHash("different string")

	if h1 != h2 {
		t.Errorf("ShortHash should be deterministic: %s != %s", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("ShortHash collision expected to be very unlikely for different strings")
	}
	if len(h1) != 8 {
		t.Errorf("ShortHash should be 8 hex chars (4 bytes): got len %d", len(h1))
	}
}

func TestParseTagInfo(t *testing.T) {
	text := "tag:myaudio id:12345 please process this file\nsecond line"
	tag, id, cleaned := ParseTagInfo(text, "mybot")

	if tag != "myaudio" {
		t.Errorf("ParseTagInfo tag = %q; want %q", tag, "myaudio")
	}
	if id != "12345" {
		t.Errorf("ParseTagInfo id = %q; want %q", id, "12345")
	}
	if !strings.Contains(cleaned, "please process this file") {
		t.Errorf("ParseTagInfo cleaned = %q; missing message body", cleaned)
	}
}

func TestGetMD5HashAndPathSize(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("antigravity go unit test content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	hash, err := GetMD5Hash(testFile)
	if err != nil {
		t.Fatalf("GetMD5Hash failed: %v", err)
	}
	if len(hash) != 32 {
		t.Errorf("GetMD5Hash returned hash of unexpected length %d", len(hash))
	}

	size, err := GetPathSize(tmpDir)
	if err != nil {
		t.Fatalf("GetPathSize failed: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("GetPathSize = %d; want %d", size, len(content))
	}
}

func TestParseContentDispositionFilename(t *testing.T) {
	tests := []struct {
		name     string
		cd       string
		expected string
	}{
		{
			name:     "empty header",
			cd:       "",
			expected: "",
		},
		{
			name:     "standard filename quotes",
			cd:       `attachment; filename="video.mp4"`,
			expected: "video.mp4",
		},
		{
			name:     "canon DDL real-world header",
			cd:       `attachment; filename="S03E04 Apothecary Diaries [HDRip] SUB [@Anime_Canon].mkv"; filename*=UTF-8''S03E04%20Apothecary%20Diaries%20%5BHDRip%5D%20SUB%20%5B@Anime_Canon%5D.mkv`,
			expected: "S03E04 Apothecary Diaries [HDRip] SUB [@Anime_Canon].mkv",
		},
		{
			name:     "RFC 5987 UTF-8 encoded with spaces",
			cd:       `inline; filename*=UTF-8''My%20Custom%20Video.mkv`,
			expected: "My Custom Video.mkv",
		},
		{
			name:     "unquoted filename",
			cd:       `attachment; filename=archive.zip`,
			expected: "archive.zip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ParseContentDispositionFilename(tt.cd)
			if res != tt.expected {
				t.Errorf("ParseContentDispositionFilename(%q) = %q; want %q", tt.cd, res, tt.expected)
			}
		})
	}
}

func TestMimeToExtension(t *testing.T) {
	if ext := MimeToExtension("video/x-matroska"); ext != ".mkv" {
		t.Errorf("MimeToExtension(video/x-matroska) = %q; want .mkv", ext)
	}
	if ext := MimeToExtension("video/mp4"); ext != ".mp4" {
		t.Errorf("MimeToExtension(video/mp4) = %q; want .mp4", ext)
	}
	if ext := MimeToExtension("application/zip"); ext != ".zip" {
		t.Errorf("MimeToExtension(application/zip) = %q; want .zip", ext)
	}
	if ext := MimeToExtension("unknown/binary"); ext != "" {
		t.Errorf("MimeToExtension(unknown/binary) = %q; want empty", ext)
	}
}

func TestDetectExtension(t *testing.T) {
	tmpDir := t.TempDir()

	// Matroska EBML
	mkvFile := filepath.Join(tmpDir, "sample_mkv")
	_ = os.WriteFile(mkvFile, []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x02, 0x03}, 0644)
	if ext := DetectExtension(mkvFile); ext != ".mkv" {
		t.Errorf("DetectExtension(mkvFile) = %q; want .mkv", ext)
	}

	// MP4
	mp4File := filepath.Join(tmpDir, "sample_mp4")
	_ = os.WriteFile(mp4File, []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, 0644)
	if ext := DetectExtension(mp4File); ext != ".mp4" {
		t.Errorf("DetectExtension(mp4File) = %q; want .mp4", ext)
	}

	// ZIP
	zipFile := filepath.Join(tmpDir, "sample_zip")
	_ = os.WriteFile(zipFile, []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x00}, 0644)
	if ext := DetectExtension(zipFile); ext != ".zip" {
		t.Errorf("DetectExtension(zipFile) = %q; want .zip", ext)
	}
}

func TestParseDurationString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty string", "", 0},
		{"N/A string", "N/A", 0},
		{"invalid string", "invalid_time", 0},
		{"seconds float", "125.500000", 125},
		{"seconds integer", "3600", 3600},
		{"HH:MM:SS format", "01:02:03", 3723},
		{"HH:MM:SS with millis", "00:23:45.500", 1425},
		{"MM:SS format", "05:30", 330},
		{"long video timestamp", "02:15:30.250", 8130},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDurationString(tt.input)
			if got != tt.expected {
				t.Errorf("parseDurationString(%q) = %d; want %d", tt.input, got, tt.expected)
			}
		})
	}
}

