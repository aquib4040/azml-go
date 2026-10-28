package ext

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ArchiveType represents the detected archive format
type ArchiveType string

const (
	ArchiveZip   ArchiveType = "zip"
	Archive7z    ArchiveType = "7z"
	ArchiveRar   ArchiveType = "rar"
	ArchiveGzip  ArchiveType = "gzip"
	ArchiveBzip2 ArchiveType = "bzip2"
	ArchiveXz    ArchiveType = "xz"
	ArchiveTar   ArchiveType = "tar"
	ArchiveIso   ArchiveType = "iso"
	ArchiveNone  ArchiveType = ""
)

// DetectArchiveByBinaryHeader reads magic binary bytes to detect if a file is an archive,
// even if the file extension is disguised, missing, or bypasses standard extension filters.
func DetectArchiveByBinaryHeader(filePath string) (bool, ArchiveType) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, ArchiveNone
	}
	defer f.Close()

	// Read up to 34KB to cover standard magic bytes, TAR header (at 257) and ISO (at 32768)
	buf := make([]byte, 36864)
	n, err := io.ReadAtLeast(f, buf, 16)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, ArchiveNone
	}
	if n < 4 {
		return false, ArchiveNone
	}

	// 1. ZIP: PK\x03\x04, PK\x05\x06 (empty), PK\x07\x08 (spanned/split)
	if buf[0] == 0x50 && buf[1] == 0x4B {
		if (buf[2] == 0x03 && buf[3] == 0x04) ||
			(buf[2] == 0x05 && buf[3] == 0x06) ||
			(buf[2] == 0x07 && buf[3] == 0x08) {
			return true, ArchiveZip
		}
	}

	// 2. 7-Zip: 7z\xBC\xAF\x27\x1C (37 7A BC AF 27 1C)
	if n >= 6 && bytes.Equal(buf[:6], []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}) {
		return true, Archive7z
	}

	// 3. RAR: Rar!\x1A\x07 (RAR 4.x and 5.x)
	if n >= 7 && bytes.Equal(buf[:6], []byte{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07}) {
		return true, ArchiveRar
	}

	// 4. GZIP: \x1F\x8B
	if buf[0] == 0x1F && buf[1] == 0x8B {
		return true, ArchiveGzip
	}

	// 5. BZIP2: BZh (42 5A 68)
	if n >= 3 && bytes.Equal(buf[:3], []byte{0x42, 0x5A, 0x68}) {
		return true, ArchiveBzip2
	}

	// 6. XZ: \xFD7zXZ\x00 (FD 37 7A 58 5A 00)
	if n >= 6 && bytes.Equal(buf[:6], []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}) {
		return true, ArchiveXz
	}

	// 7. TAR: "ustar" magic string at byte offset 257
	if n >= 262 && bytes.Equal(buf[257:262], []byte("ustar")) {
		return true, ArchiveTar
	}

	// 8. ISO 9660: "CD001" magic string at byte offset 32769 (0x8001)
	if n >= 32774 && bytes.Equal(buf[32769:32774], []byte("CD001")) {
		return true, ArchiveIso
	}

	return false, ArchiveNone
}

var (
	firstSplitRegex = regexp.MustCompile(`(?i)(\.|_)(part0*1\.rar|7z\.0*1|zip\.0*1)$`)
	partRarRegex    = regexp.MustCompile(`(?i)(\.|_)part\d+\.rar$`)
	splitRegex      = regexp.MustCompile(`(?i)\.r\d+$|\.7z\.\d+$|\.z\d+$|\.zip\.\d+$`)
)

// IsFirstArchiveSplit checks if the file is the first part of a split archive or a standalone/main RAR.
func IsFirstArchiveSplit(filePath string) bool {
	base := filepath.Base(filePath)
	if firstSplitRegex.MatchString(base) {
		return true
	}
	if strings.HasSuffix(strings.ToLower(base), ".rar") && !partRarRegex.MatchString(base) {
		return true
	}
	return false
}

// IsArchiveSplit checks if the file is a part of a split archive.
func IsArchiveSplit(filePath string) bool {
	base := filepath.Base(filePath)
	return splitRegex.MatchString(base) || partRarRegex.MatchString(base)
}

// GetArchiveBaseName returns the base name of an archive, stripping split suffixes or archive extensions.
func GetArchiveBaseName(filePath string) string {
	name := filepath.Base(filePath)
	if IsFirstArchiveSplit(name) {
		loc := regexp.MustCompile(`(?i)(\.|_)(part0*1\.rar|7z\.0*1|zip\.0*1)$`).FindStringIndex(name)
		if len(loc) == 2 {
			return name[:loc[0]]
		}
	}
	ext := filepath.Ext(name)
	if ext != "" {
		lower := strings.ToLower(name)
		for _, sfx := range []string{".tar.gz", ".tar.xz", ".tar.bz2"} {
			if strings.HasSuffix(lower, sfx) {
				return name[:len(name)-len(sfx)]
			}
		}
		return strings.TrimSuffix(name, ext)
	}
	return name
}

// IsArchive checks both binary magic header and common file extensions.
func IsArchive(filePath string) (bool, ArchiveType) {
	if isArch, archType := DetectArchiveByBinaryHeader(filePath); isArch {
		return true, archType
	}

	if IsFirstArchiveSplit(filePath) || IsArchiveSplit(filePath) {
		return true, Archive7z
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".zip", ".apk", ".jar", ".docx", ".xlsx", ".pptx":
		return true, ArchiveZip
	case ".7z":
		return true, Archive7z
	case ".rar":
		return true, ArchiveRar
	case ".tar":
		return true, ArchiveTar
	case ".gz", ".tgz":
		return true, ArchiveGzip
	case ".bz2", ".tbz2":
		return true, ArchiveBzip2
	case ".xz", ".txz":
		return true, ArchiveXz
	case ".iso":
		return true, ArchiveIso
	}

	name := strings.ToLower(filepath.Base(filePath))
	if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.xz") || strings.HasSuffix(name, ".tar.bz2") {
		return true, ArchiveTar
	}

	return false, ArchiveNone
}

// InspectArchiveSafety inspects an archive before extraction to detect Zip Bombs,
// excessive file counts, or uncompressed expansion exceeding server disk capacity.
func InspectArchiveSafety(archivePath, password string) (uncompressedSize int64, fileCount int, isZipBomb bool, err error) {
	fi, sErr := os.Stat(archivePath)
	if sErr != nil {
		return 0, 0, false, sErr
	}
	compressedSize := fi.Size()

	args := []string{"l", "-slt"}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p-")
	}
	args = append(args, archivePath)
	cmd := exec.Command("7z", args...)
	output, lErr := cmd.Output()
	if lErr != nil {
		// If listing fails (e.g. encrypted headers or unknown format), proceed with caution
		return compressedSize, 1, false, nil
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Size = ") {
			szStr := strings.TrimPrefix(line, "Size = ")
			if sz, err := strconv.ParseInt(strings.TrimSpace(szStr), 10, 64); err == nil {
				uncompressedSize += sz
			}
		} else if strings.HasPrefix(line, "Path = ") {
			fileCount++
		}
	}

	// Zip Bomb Check: Expansion ratio > 50x for files larger than 1MB
	if compressedSize > 1024*1024 && uncompressedSize > 0 {
		ratio := float64(uncompressedSize) / float64(compressedSize)
		if ratio > 50.0 {
			log.Printf("[ZIP BOMB WARNING] File %s has dangerous compression ratio: %.1fx (compressed: %d, uncompressed: %d)",
				archivePath, ratio, compressedSize, uncompressedSize)
			return uncompressedSize, fileCount, true, nil
		}
	}

	// Excessive file count check (anti DOS bomb)
	if fileCount > 25000 {
		log.Printf("[ARCHIVE BOMB] File %s contains over %d files, potential bomb!", archivePath, fileCount)
		return uncompressedSize, fileCount, true, nil
	}

	return uncompressedSize, fileCount, false, nil
}

func findHatchZipBin() string {
	if p, err := exec.LookPath("hatchzip"); err == nil {
		return p
	}
	if p, err := exec.LookPath("hz"); err == nil {
		return p
	}
	return ""
}

// GetExtractEngineName returns "HatchZip" if hatchzip or hz binary is found, otherwise "7-Zip".
func GetExtractEngineName() string {
	if findHatchZipBin() != "" {
		return "HatchZip"
	}
	return "7-Zip"
}

func cleanHatchZipMeta(archivePath string) {
	_ = os.Remove(archivePath + ".hatchzip")
	_ = os.Remove(archivePath + ".hatchzip-tail")
}

// ForceExtractArchive extracts any archive using in-place HatchZip (with auto-restore)
// and falls back to 7z with Zip Bomb and disk safety checks.
func ForceExtractArchive(archivePath, targetDir, password string) (string, error) {
	// 1. Zip Bomb & Safety Pre-check
	uncompressedSize, count, isBomb, _ := InspectArchiveSafety(archivePath, password)
	if isBomb {
		return "", fmt.Errorf("⌬ Zip Bomb Protection: archive has abnormal expansion ratio or excessive files (%d files, %s unpacked). Extraction blocked",
			count, GetReadableFileSize(uncompressedSize))
	}

	// 2. Storage Threshold Pre-check
	if uncompressedSize > 0 && !CheckStorageThreshold(uncompressedSize, true) {
		return "", fmt.Errorf("⌬ Insufficient storage: extracting requires %s which exceeds safe storage limits",
			GetReadableFileSize(uncompressedSize))
	}

	_ = os.MkdirAll(targetDir, 0755)

	// 3. Attempt HatchZip in-place extraction for disk savings
	hzBin := findHatchZipBin()
	if hzBin != "" {
		hzArgs := []string{"extract", archivePath, "-o", targetDir, "-y", "--replace"}
		if password != "" {
			hzArgs = append(hzArgs, "-p", password)
		}

		log.Printf("[ARCHIVE] Attempting HatchZip in-place extraction: %s -> %s...", archivePath, targetDir)
		hzCmd := exec.Command(hzBin, hzArgs...)
		hzOutput, hzErr := hzCmd.CombinedOutput()
		if hzErr == nil {
			log.Printf("[ARCHIVE] HatchZip in-place extraction successful for: %s", archivePath)
			cleanHatchZipMeta(archivePath)
			return targetDir, nil
		}

		log.Printf("[ARCHIVE] HatchZip failed (err: %v, output: %s). Attempting to restore archive and falling back to 7z...",
			hzErr, strings.TrimSpace(string(hzOutput)))
		restoreCmd := exec.Command(hzBin, "restore", archivePath, "-o", targetDir, "-q")
		_ = restoreCmd.Run()
		cleanHatchZipMeta(archivePath)
	}

	// 4. Fallback: 7-Zip extraction
	args := []string{"x", "-y", archivePath, "-o" + targetDir, "-aot", "-xr!@PaxHeader"}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p-") // Do not prompt for password
	}

	log.Printf("[ARCHIVE] Falling back to 7z extraction for %s (uncompressed: %s, %d files) -> %s...",
		archivePath, GetReadableFileSize(uncompressedSize), count, targetDir)
	cmd := exec.Command("7z", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ARCHIVE] 7z error: %v, output: %s", err, string(output))
		return "", fmt.Errorf("extraction failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	cleanHatchZipMeta(archivePath)
	log.Printf("[ARCHIVE] 7z extraction successful for %s", archivePath)
	return targetDir, nil
}

// CompressToZip compresses a file or directory into a standard zip archive using 7z.
func CompressToZip(sourcePath, targetZipPath, password string) error {
	_ = os.MkdirAll(filepath.Dir(targetZipPath), 0755)

	args := []string{"a", "-tzip", "-mx=1", targetZipPath, sourcePath}
	if password != "" {
		args = append(args, "-p"+password)
	}

	log.Printf("[ARCHIVE] Compressing %s -> %s...", sourcePath, targetZipPath)
	cmd := exec.Command("7z", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ARCHIVE] 7z zip error: %v, output: %s", err, string(output))
		return fmt.Errorf("compression failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
