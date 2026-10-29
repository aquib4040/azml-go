package ext

import (
	"os"
	"testing"
)

func TestBotSettingsPersistence(t *testing.T) {
	tmpFile := "test_bot_settings.json"
	defer os.Remove(tmpFile)

	mgr := &DatabaseManager{
		localSettingsFile: tmpFile,
	}

	// 1. Save settings
	if err := mgr.SaveBotSetting("USER_SESSION_STRING", "1BVts9test_session_string"); err != nil {
		t.Fatalf("failed to save USER_SESSION_STRING: %v", err)
	}
	if err := mgr.SaveBotSetting("DUMP_CHAT_ID", "-1001234567890"); err != nil {
		t.Fatalf("failed to save DUMP_CHAT_ID: %v", err)
	}

	// 2. Load all settings
	settings, err := mgr.LoadAllBotSettings()
	if err != nil {
		t.Fatalf("failed to load bot settings: %v", err)
	}

	if settings["USER_SESSION_STRING"] != "1BVts9test_session_string" {
		t.Errorf("expected '1BVts9test_session_string', got '%s'", settings["USER_SESSION_STRING"])
	}
	if settings["DUMP_CHAT_ID"] != "-1001234567890" {
		t.Errorf("expected '-1001234567890', got '%s'", settings["DUMP_CHAT_ID"])
	}

	// 3. Get single setting
	val, err := mgr.GetBotSetting("USER_SESSION_STRING")
	if err != nil || val != "1BVts9test_session_string" {
		t.Errorf("GetBotSetting failed, expected '1BVts9test_session_string', got '%s' (err: %v)", val, err)
	}

	// 4. Delete setting (by setting to empty)
	if err := mgr.SaveBotSetting("USER_SESSION_STRING", ""); err != nil {
		t.Fatalf("failed to reset setting: %v", err)
	}

	settingsAfter, _ := mgr.LoadAllBotSettings()
	if _, ok := settingsAfter["USER_SESSION_STRING"]; ok {
		t.Errorf("expected USER_SESSION_STRING to be removed from settings map")
	}
}
