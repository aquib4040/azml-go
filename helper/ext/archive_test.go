package ext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveSplitDetection(t *testing.T) {
	firstSplits := []string{
		"archive.part01.rar",
		"archive.part1.rar",
		"movie.part001.rar",
		"package.7z.001",
		"package.7z.01",
		"backup.zip.001",
		"single_archive.rar",
	}

	for _, name := range firstSplits {
		if !IsFirstArchiveSplit(name) {
			t.Errorf("IsFirstArchiveSplit(%q) = false; want true", name)
		}
	}

	subsequentSplits := []string{
		"archive.part02.rar",
		"archive.part2.rar",
		"package.7z.002",
		"backup.zip.002",
		"archive.r00",
		"archive.r01",
		"archive.z01",
		"archive.z02",
	}

	for _, name := range subsequentSplits {
		if IsFirstArchiveSplit(name) && name != "archive.part02.rar" && name != "archive.part2.rar" {
			t.Errorf("IsFirstArchiveSplit(%q) = true; want false", name)
		}
		if !IsArchiveSplit(name) {
			t.Errorf("IsArchiveSplit(%q) = false; want true", name)
		}
	}
}

func TestGetArchiveBaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"movie.part01.rar", "movie"},
		{"movie.part1.rar", "movie"},
		{"backup.7z.001", "backup"},
		{"data.zip.001", "data"},
		{"archive.tar.gz", "archive"},
		{"archive.tar.xz", "archive"},
		{"archive.tar.bz2", "archive"},
		{"standalone.zip", "standalone"},
		{"standalone.7z", "standalone"},
		{"regular_video.mkv", "regular_video"},
	}

	for _, tt := range tests {
		got := GetArchiveBaseName(tt.input)
		if got != tt.expected {
			t.Errorf("GetArchiveBaseName(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestGetExtractEngineName(t *testing.T) {
	engine := GetExtractEngineName()
	if engine != "HatchZip" && engine != "7-Zip" {
		t.Errorf("GetExtractEngineName() = %q; want 'HatchZip' or '7-Zip'", engine)
	}
}

func TestCleanHatchZipMeta(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "sample.zip")
	_ = os.WriteFile(archivePath, []byte("data"), 0644)
	_ = os.WriteFile(archivePath+".hatchzip", []byte("hzmeta"), 0644)
	_ = os.WriteFile(archivePath+".hatchzip-tail", []byte("hztail"), 0644)

	cleanHatchZipMeta(archivePath)

	if _, err := os.Stat(archivePath + ".hatchzip"); !os.IsNotExist(err) {
		t.Errorf("cleanHatchZipMeta failed to remove .hatchzip")
	}
	if _, err := os.Stat(archivePath + ".hatchzip-tail"); !os.IsNotExist(err) {
		t.Errorf("cleanHatchZipMeta failed to remove .hatchzip-tail")
	}
}
