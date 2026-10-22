package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"azml-go/helper/downloader"
)

type SelectionStoreItem struct {
	Files       []downloader.GDriveFile `json:"files"`
	SelectedIDs []string                `json:"selected_ids"`
}

type MegaSelectionStoreItem struct {
	Files       []downloader.MegaFileInfo `json:"files"`
	SelectedIDs []string                  `json:"selected_ids"`
}

type GDriveSelectionStore struct {
	data sync.Map
}

type MegaSelectionStore struct {
	data sync.Map
}

var (
	GDriveStore = &GDriveSelectionStore{}
	MegaStore   = &MegaSelectionStore{}
	baseStoreDir = "/usr/src/app/downloads/.selections"
)

func init() {
	_ = os.MkdirAll(baseStoreDir, 0755)
}

// ── GDrive Selection Store ──

func (s *GDriveSelectionStore) SaveData(gid string, files []downloader.GDriveFile) {
	s.data.Store(gid+"_files", files)
	var allIDs []string
	for idx := range files {
		if !files[idx].IsDir {
			allIDs = append(allIDs, files[idx].ID)
		}
	}
	s.data.Store(gid+"_sel", allIDs)

	// Save to disk cache
	filePath := filepath.Join(baseStoreDir, "gd_"+gid+".json")
	payload := SelectionStoreItem{Files: files, SelectedIDs: allIDs}
	if b, err := json.Marshal(payload); err == nil {
		_ = os.WriteFile(filePath, b, 0644)
	}
}

func (s *GDriveSelectionStore) GetData(gid string) []downloader.GDriveFile {
	if val, ok := s.data.Load(gid + "_files"); ok {
		if files, ok := val.([]downloader.GDriveFile); ok {
			return files
		}
	}
	// Try disk fallback
	filePath := filepath.Join(baseStoreDir, "gd_"+gid+".json")
	if b, err := os.ReadFile(filePath); err == nil {
		var item SelectionStoreItem
		if err := json.Unmarshal(b, &item); err == nil {
			s.data.Store(gid+"_files", item.Files)
			s.data.Store(gid+"_sel", item.SelectedIDs)
			return item.Files
		}
	}
	return nil
}

func (s *GDriveSelectionStore) SaveSelection(gid string, selectedIDs []string) {
	s.data.Store(gid+"_sel", selectedIDs)
	files := s.GetData(gid)
	if files != nil {
		for i := range files {
			if !files[i].IsDir {
				files[i].Selected = contains(selectedIDs, files[i].ID)
			}
		}
		s.data.Store(gid+"_files", files)
		filePath := filepath.Join(baseStoreDir, "gd_"+gid+".json")
		payload := SelectionStoreItem{Files: files, SelectedIDs: selectedIDs}
		if b, err := json.Marshal(payload); err == nil {
			_ = os.WriteFile(filePath, b, 0644)
		}
	}
}

func (s *GDriveSelectionStore) ReadSelection(gid string) []string {
	if val, ok := s.data.Load(gid + "_sel"); ok {
		if ids, ok := val.([]string); ok {
			return ids
		}
	}
	return nil
}

func (s *GDriveSelectionStore) DeleteData(gid string) {
	s.data.Delete(gid + "_files")
	s.data.Delete(gid + "_sel")
	_ = os.Remove(filepath.Join(baseStoreDir, "gd_"+gid+".json"))
}

// ── Mega Selection Store ──

func (s *MegaSelectionStore) SaveData(gid string, files []downloader.MegaFileInfo) {
	s.data.Store(gid+"_files", files)
	var allIDs []string
	for idx := range files {
		if !files[idx].IsDir {
			allIDs = append(allIDs, files[idx].ID)
		}
	}
	s.data.Store(gid+"_sel", allIDs)

	filePath := filepath.Join(baseStoreDir, "mega_"+gid+".json")
	payload := MegaSelectionStoreItem{Files: files, SelectedIDs: allIDs}
	if b, err := json.Marshal(payload); err == nil {
		_ = os.WriteFile(filePath, b, 0644)
	}
}

func (s *MegaSelectionStore) GetData(gid string) []downloader.MegaFileInfo {
	if val, ok := s.data.Load(gid + "_files"); ok {
		if files, ok := val.([]downloader.MegaFileInfo); ok {
			return files
		}
	}
	filePath := filepath.Join(baseStoreDir, "mega_"+gid+".json")
	if b, err := os.ReadFile(filePath); err == nil {
		var item MegaSelectionStoreItem
		if err := json.Unmarshal(b, &item); err == nil {
			s.data.Store(gid+"_files", item.Files)
			s.data.Store(gid+"_sel", item.SelectedIDs)
			return item.Files
		}
	}
	return nil
}

func (s *MegaSelectionStore) SaveSelection(gid string, selectedIDs []string) {
	s.data.Store(gid+"_sel", selectedIDs)
	files := s.GetData(gid)
	if files != nil {
		for i := range files {
			if !files[i].IsDir {
				files[i].Selected = contains(selectedIDs, files[i].ID)
			}
		}
		s.data.Store(gid+"_files", files)
		filePath := filepath.Join(baseStoreDir, "mega_"+gid+".json")
		payload := MegaSelectionStoreItem{Files: files, SelectedIDs: selectedIDs}
		if b, err := json.Marshal(payload); err == nil {
			_ = os.WriteFile(filePath, b, 0644)
		}
	}
}

func (s *MegaSelectionStore) ReadSelection(gid string) []string {
	if val, ok := s.data.Load(gid + "_sel"); ok {
		if ids, ok := val.([]string); ok {
			return ids
		}
	}
	return nil
}

func (s *MegaSelectionStore) DeleteData(gid string) {
	s.data.Delete(gid + "_files")
	s.data.Delete(gid + "_sel")
	_ = os.Remove(filepath.Join(baseStoreDir, "mega_"+gid+".json"))
}

func contains(arr []string, target string) bool {
	for _, a := range arr {
		if a == target {
			return true
		}
	}
	return false
}
