package config

import (
	"os"
	"reflect"
	"testing"
)

func TestGetEnv(t *testing.T) {
	const key = "TEST_AZML_ENV_KEY"
	os.Unsetenv(key)

	if val := getEnv(key, "default_val"); val != "default_val" {
		t.Errorf("getEnv with unset key = %q; want %q", val, "default_val")
	}

	os.Setenv(key, "  custom_value  ")
	defer os.Unsetenv(key)

	if val := getEnv(key, "default_val"); val != "custom_value" {
		t.Errorf("getEnv with set key = %q; want %q", val, "custom_value")
	}
}

func TestGetEnvInt(t *testing.T) {
	const key = "TEST_AZML_INT_KEY"
	os.Unsetenv(key)

	if val := getEnvInt(key, 42); val != 42 {
		t.Errorf("getEnvInt with unset key = %d; want 42", val)
	}

	os.Setenv(key, "100")
	if val := getEnvInt(key, 42); val != 100 {
		t.Errorf("getEnvInt with valid int = %d; want 100", val)
	}

	os.Setenv(key, "invalid_number")
	if val := getEnvInt(key, 42); val != 42 {
		t.Errorf("getEnvInt with invalid int = %d; want default 42", val)
	}
	os.Unsetenv(key)
}

func TestGetEnvInt64(t *testing.T) {
	const key = "TEST_AZML_INT64_KEY"
	os.Unsetenv(key)

	if val := getEnvInt64(key, 9999999999); val != 9999999999 {
		t.Errorf("getEnvInt64 with unset key = %d; want 9999999999", val)
	}

	os.Setenv(key, "1234567890123")
	if val := getEnvInt64(key, 999); val != 1234567890123 {
		t.Errorf("getEnvInt64 with valid int64 = %d; want 1234567890123", val)
	}

	os.Setenv(key, "invalid_num")
	if val := getEnvInt64(key, 999); val != 999 {
		t.Errorf("getEnvInt64 with invalid int64 = %d; want default 999", val)
	}
	os.Unsetenv(key)
}

func TestGetEnvBool(t *testing.T) {
	const key = "TEST_AZML_BOOL_KEY"
	os.Unsetenv(key)

	if val := getEnvBool(key, false); val != false {
		t.Errorf("getEnvBool with unset key = %v; want false", val)
	}

	truthyValues := []string{"true", "1", "yes", "t", "TRUE", "True"}
	for _, v := range truthyValues {
		os.Setenv(key, v)
		if !getEnvBool(key, false) {
			t.Errorf("getEnvBool(%q) = false; want true", v)
		}
	}

	falsyValues := []string{"false", "0", "no", "f", "random"}
	for _, v := range falsyValues {
		os.Setenv(key, v)
		if getEnvBool(key, true) {
			t.Errorf("getEnvBool(%q) = true; want false", v)
		}
	}
	os.Unsetenv(key)
}

func TestParseIDList(t *testing.T) {
	input := "12345 67890 -100200 invalid 999"
	expected := []int64{12345, 67890, -100200, 999}
	result := parseIDList(input)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("parseIDList(%q) = %v; want %v", input, result, expected)
	}

	emptyResult := parseIDList("")
	if len(emptyResult) != 0 {
		t.Errorf("parseIDList(\"\") returned non-empty slice: %v", emptyResult)
	}
}

func TestCleanDir(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"downloads", "downloads/"},
		{"downloads/", "downloads/"},
		{"downloads\\", "downloads\\"},
	}

	for _, tt := range tests {
		result := cleanDir(tt.input)
		if result != tt.expected {
			t.Errorf("cleanDir(%q) = %q; want %q", tt.input, result, tt.expected)
		}
	}
}

func TestMaskSession(t *testing.T) {
	if got := MaskSession(""); got != "Not Set" {
		t.Errorf("MaskSession(\"\") = %q; want %q", got, "Not Set")
	}

	short := "12345"
	if got := MaskSession(short); got != "*** (Set)" {
		t.Errorf("MaskSession(%q) = %q; want %q", short, got, "*** (Set)")
	}

	long := "1BVts999999999999999xyz123"
	got := MaskSession(long)
	if got != "1BVts9...z123 (Set)" {
		t.Errorf("MaskSession(%q) = %q; want %q", long, got, "1BVts9...z123 (Set)")
	}
}

func TestUpdateConfigVarUserSessionAndDump(t *testing.T) {
	testSess := "1BVtsCustomSessionStringSample"
	_ = UpdateConfigVar("USER_SESSION_STRING", testSess)
	cfg := Get()
	if cfg.UserSessionString != testSess {
		t.Errorf("GlobalConfig.UserSessionString = %q; want %q", cfg.UserSessionString, testSess)
	}

	_ = UpdateConfigVar("DUMP_CHAT_ID", "-1001234567890")
	if cfg.DumpChatID != -1001234567890 {
		t.Errorf("GlobalConfig.DumpChatID = %d; want %d", cfg.DumpChatID, -1001234567890)
	}

	m := GetConfigMap()
	if m["USER_SESSION_STRING"] != MaskSession(testSess) {
		t.Errorf("GetConfigMap() USER_SESSION_STRING = %q; want %q", m["USER_SESSION_STRING"], MaskSession(testSess))
	}
	if m["DUMP_CHAT_ID"] != "-1001234567890" {
		t.Errorf("GetConfigMap() DUMP_CHAT_ID = %q; want %q", m["DUMP_CHAT_ID"], "-1001234567890")
	}

	// Reset
	_ = UpdateConfigVar("USER_SESSION_STRING", "")
	_ = UpdateConfigVar("DUMP_CHAT_ID", "0")
}
