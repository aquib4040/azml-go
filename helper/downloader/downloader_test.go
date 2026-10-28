package downloader

import (
	"os"
	"testing"
	"time"
)

func TestComputeTargetWorkers(t *testing.T) {
	os.Unsetenv("DOWNLOAD_THREADS")

	// 1 active download
	if w := computeTargetWorkers(1, 10, 100); w != 6 {
		t.Errorf("expected 6 workers for 1 active download, got %d", w)
	}

	// 2-4 active downloads (configured concurrency = 4)
	for dls := int64(2); dls <= 4; dls++ {
		if w := computeTargetWorkers(dls, 10, 100); w != 5 {
			t.Errorf("expected 5 workers for %d active downloads, got %d", dls, w)
		}
	}

	// 5+ active downloads
	if w := computeTargetWorkers(6, 10, 100); w != 4 {
		t.Errorf("expected 4 workers for 6 active downloads, got %d", w)
	}

	// Environment variable override
	os.Setenv("DOWNLOAD_THREADS", "7")
	if w := computeTargetWorkers(4, 10, 100); w != 7 {
		t.Errorf("expected 7 workers with DOWNLOAD_THREADS=7, got %d", w)
	}
	os.Unsetenv("DOWNLOAD_THREADS")
}

func TestTelegramChunkDownloaderDecayPacing(t *testing.T) {
	d := NewTelegramChunkDownloader(nil)

	// Base should be 50ms
	base := d.minIntervalNs.Load()
	if base != int64(50*time.Millisecond) {
		t.Errorf("expected baseline 50ms, got %v", time.Duration(base))
	}

	// Simulate FloodWait increase
	d.minIntervalNs.Store(int64(100 * time.Millisecond))
	d.decayPacing()

	afterDecay := d.minIntervalNs.Load()
	if afterDecay != int64(95*time.Millisecond) {
		t.Errorf("expected 95ms after 1 decay, got %v", time.Duration(afterDecay))
	}

	// Decay multiple times until baseline
	for i := 0; i < 20; i++ {
		d.decayPacing()
	}

	final := d.minIntervalNs.Load()
	if final != int64(50*time.Millisecond) {
		t.Errorf("expected decay to floor at 50ms, got %v", time.Duration(final))
	}
}
