package ext

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"azml-go/config"
)

// CleanDownloadsDir cleans up stale leftover files in the download directory on startup
func CleanDownloadsDir() {
	cfg := config.Get()
	dlDir := cfg.DownloadDir
	if dlDir == "" {
		dlDir = "/usr/src/app/downloads/"
	}

	log.Printf("[CLEANUP] Cleaning downloads directory: %s...", dlDir)
	entries, err := os.ReadDir(dlDir)
	if err != nil {
		_ = os.MkdirAll(dlDir, 0755)
		return
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dlDir, entry.Name())
		_ = os.RemoveAll(fullPath)
	}
	_ = os.RemoveAll("Thumbnails")
	_ = os.MkdirAll("Thumbnails", 0755)
	_ = os.MkdirAll(dlDir, 0755)
	log.Println("[CLEANUP] Downloads directory initialized clean.")
}

// StartPeriodicStorageCleaner runs in the background to safely monitor storage.
// It will NEVER delete active tasks or long-running torrents. It only purges truly orphaned
// folders with no active tasks in TaskManager and only if disk space drops below safety thresholds.
func StartPeriodicStorageCleaner() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			cfg := config.Get()
			dlDir := cfg.DownloadDir
			if dlDir == "" {
				continue
			}

			// Check if disk space is actually low before attempting any cleanup
			freeBytes := GetFreeDiskSpace(dlDir)
			thresholdBytes := cfg.StorageThreshold * 1024 * 1024 * 1024
			if freeBytes > thresholdBytes*2 {
				// Ample free disk space, do not delete anything
				continue
			}

			entries, err := os.ReadDir(dlDir)
			if err != nil {
				continue
			}

			// Gather all active task names and GIDs from TaskManager
			activeNames := make(map[string]bool)
			if tm := GlobalTaskManager; tm != nil {
				for _, t := range tm.GetAllTasks() {
					if n := t.Name(); n != "" {
						activeNames[n] = true
					}
					if gid := t.GID(); gid != "" {
						activeNames[gid] = true
					}
				}
			}

			now := time.Now()
			for _, entry := range entries {
				fullPath := filepath.Join(dlDir, entry.Name())

				// If directory belongs to any currently active task, NEVER delete it!
				if activeNames[entry.Name()] || activeNames[fullPath] {
					continue
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				// Only consider orphaned folders with zero write activity for over 78 hours
				if now.Sub(info.ModTime()) > 78*time.Hour {
					log.Printf("[EMERGENCY CLEANUP] Purging inactive orphaned folder to preserve disk space: %s (idle for %v)",
						fullPath, now.Sub(info.ModTime()).Round(time.Hour))
					_ = os.RemoveAll(fullPath)
				}
			}
		}
	}()
}
