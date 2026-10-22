package config

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken                  string
	OwnerID                   int64
	TelegramAPI               int
	TelegramHash              string
	UserSessionString         string
	DatabaseURL               string
	DownloadDir               string
	CmdSuffix                 string
	AuthorizedChats           []int64
	SudoUsers                 []int64
	BlacklistUsers            []int64
	StatusLimit               int
	StatusUpdateInterval      int
	AutoDeleteMessageDuration int
	LeechSplitSize            int64
	EqualSplits               bool
	AsDocument                bool
	MediaGroup                bool
	CapFont                   string
	LeechFilenamePrefix       string
	LeechFilenameSuffix       string
	LeechFilenameCaption      string
	LeechFilenameRemname      string
	LeechFilenameRemnameAuto  string
	LeechFilenameRemnameRegex string
	LeechLogID                int64
	LinksLogID                int64
	TorrentTimeout            int
	BaseURL                   string
	BaseURLPort               int
	MegaEmail                 string
	MegaPassword              string
	TokenTimeout              int64
	LoginPass                 string
	Shortener                 string
	ShortenerAPI              string
	ShortenerEnabled          bool
	TokenInPM                 bool
	ScreenshotsMode           bool
	ShowMediaInfo             bool
	SaveMsg                   bool
	SourceLink                bool
	Metadata                  string
	TitleName                 string
	AuthorName                string
	AuthorURL                 string
	BotTheme                  string
	ShowExtraCmds             bool
	Timezone                  string
	EnableFFmpegCmds          bool
	FFmpegEncoding            bool
	TMDBAccessToken           string
	AutoThumbnail             bool
	CoverImage                string
	DDLAPI                    string
	QueueAll                  int
	QueueDownload             int
	QueueUpload               int
	LeechLimit                int64 // GB
	TorrentLimit              int64 // GB
	DirectLimit               int64 // GB
	YtdlpLimit                int64 // GB
	ArchiveLimit              int64 // GB
	ExtractLimit              int64 // GB
	DailyLeechLimit           int64 // GB
	DailyTaskLimit            int   // count
	StorageThreshold          int64 // GB
}

var (
	GlobalConfig  Config
	configLock    sync.RWMutex
	MaxSplitSize  int64 = 2097152000 // 1.91 GB default
	IsPremiumUser bool  = false
)

func SetIsPremiumUser(isPrem bool) {
	configLock.Lock()
	defer configLock.Unlock()
	IsPremiumUser = isPrem
	if isPrem {
		MaxSplitSize = 4194304000 // 3.91 GB
	} else {
		MaxSplitSize = 2097152000 // 1.91 GB
	}
	if GlobalConfig.LeechSplitSize == 0 || GlobalConfig.LeechSplitSize > MaxSplitSize || GlobalConfig.LeechSplitSize == 2097152000 || GlobalConfig.LeechSplitSize == 4194304000 {
		GlobalConfig.LeechSplitSize = MaxSplitSize
	}
}

func GetMaxSplitSize() int64 {
	configLock.RLock()
	defer configLock.RUnlock()
	return MaxSplitSize
}

func LoadConfig(path string) (*Config, error) {
	configLock.Lock()
	defer configLock.Unlock()

	_ = godotenv.Load(path)
	_ = godotenv.Load(".env")
	_ = godotenv.Load("config.env")

	GlobalConfig = Config{
		BotToken:                  getEnv("BOT_TOKEN", ""),
		OwnerID:                   getEnvInt64("OWNER_ID", 0),
		TelegramAPI:               getEnvInt("TELEGRAM_API", 0),
		TelegramHash:              getEnv("TELEGRAM_HASH", ""),
		UserSessionString:         getEnv("USER_SESSION_STRING", ""),
		DatabaseURL:               getEnv("DATABASE_URL", ""),
		DownloadDir:               cleanDir(getEnv("DOWNLOAD_DIR", "/usr/src/app/downloads/")),
		CmdSuffix:                 getEnv("CMD_SUFFIX", ""),
		AuthorizedChats:           parseIDList(getEnv("AUTHORIZED_CHATS", "")),
		SudoUsers:                 parseIDList(getEnv("SUDO_USERS", "")),
		BlacklistUsers:            parseIDList(getEnv("BLACKLIST_USERS", "")),
		StatusLimit:               getEnvInt("STATUS_LIMIT", 4),
		StatusUpdateInterval:      getEnvInt("STATUS_UPDATE_INTERVAL", 10),
		AutoDeleteMessageDuration: getEnvInt("AUTO_DELETE_MESSAGE_DURATION", 60),
		LeechSplitSize:            getEnvInt64("LEECH_SPLIT_SIZE", 2097152000), // ~2GB
		EqualSplits:               getEnvBool("EQUAL_SPLITS", false),
		AsDocument:                getEnvBool("AS_DOCUMENT", false),
		MediaGroup:                getEnvBool("MEDIA_GROUP", false),
		CapFont:                   getEnv("CAP_FONT", "code"),
		LeechFilenamePrefix:       getEnv("LEECH_FILENAME_PREFIX", ""),
		LeechFilenameSuffix:       getEnv("LEECH_FILENAME_SUFFIX", ""),
		LeechFilenameCaption:      getEnv("LEECH_FILENAME_CAPTION", ""),
		LeechFilenameRemname:      getEnv("LEECH_FILENAME_REMNAME", ""),
		LeechFilenameRemnameAuto:  getEnv("LEECH_FILENAME_REMNAME_AUTO", ""),
		LeechFilenameRemnameRegex: getEnv("LEECH_FILENAME_REMNAME_REGEX", ""),
		LeechLogID:                getEnvInt64("LEECH_LOG_ID", 0),
		LinksLogID:                getEnvInt64("LINKS_LOG_ID", 0),
		TorrentTimeout:            getEnvInt("TORRENT_TIMEOUT", 0),
		BaseURL:                   getEnv("BASE_URL", ""),
		BaseURLPort:               getEnvInt("PORT", getEnvInt("BASE_URL_PORT", 23434)),
		MegaEmail:                 getEnv("MEGA_EMAIL", ""),
		MegaPassword:              getEnv("MEGA_PASSWORD", ""),
		TokenTimeout:              getEnvInt64("TOKEN_TIMEOUT", 0),
		LoginPass:                 getEnv("LOGIN_PASS", ""),
		Shortener:                 getEnv("SHORTENER", ""),
		ShortenerAPI:              getEnv("SHORTENER_API", ""),
		ShortenerEnabled:          getEnvBool("SHORTENER_ENABLED", true),
		TokenInPM:                 getEnvBool("TOKEN_IN_PM", true),
		ScreenshotsMode:           getEnvBool("SCREENSHOTS_MODE", false),
		ShowMediaInfo:             getEnvBool("SHOW_MEDIAINFO", false),
		SaveMsg:                   getEnvBool("SAVE_MSG", false),
		SourceLink:                getEnvBool("SOURCE_LINK", false),
		Metadata:                  getEnv("METADATA", ""),
		TitleName:                 getEnv("TITLE_NAME", "AZML-Go"),
		AuthorName:                getEnv("AUTHOR_NAME", "AZML-Go"),
		AuthorURL:                 getEnv("AUTHOR_URL", "https://t.me/CANON_BOTS"),
		BotTheme:                  getEnv("BOT_THEME", "minimal"),
		ShowExtraCmds:             getEnvBool("SHOW_EXTRA_CMDS", true),
		Timezone:                  getEnv("TIMEZONE", "Asia/Kolkata"),
		EnableFFmpegCmds:          getEnvBool("ENABLE_FFMPEG_CMDS", true),
		FFmpegEncoding:            getEnvBool("FFMPEG_ENCODING", false),
		TMDBAccessToken:           getEnv("TMDB_ACCESS_TOKEN", ""),
		AutoThumbnail:             getEnvBool("AUTO_THUMBNAIL", false),
		CoverImage:                getEnv("COVER_IMAGE", "https://graph.org/file/60f9f8bcb97d27f76f5c0.jpg"),
		DDLAPI:                    getEnv("DDL_API", getEnv("THUNDER_API", "")),
		QueueAll:                  getEnvInt("QUEUE_ALL", 0),
		QueueDownload:             getEnvInt("QUEUE_DOWNLOAD", 0),
		QueueUpload:               getEnvInt("QUEUE_UPLOAD", 0),
		LeechLimit:                getEnvInt64("LEECH_LIMIT", 0),
		TorrentLimit:              getEnvInt64("TORRENT_LIMIT", 0),
		DirectLimit:               getEnvInt64("DIRECT_LIMIT", 0),
		YtdlpLimit:                getEnvInt64("YTDLP_LIMIT", 0),
		ArchiveLimit:              getEnvInt64("ARCHIVE_LIMIT", 0),
		ExtractLimit:              getEnvInt64("EXTRACT_LIMIT", 0),
		DailyLeechLimit:           getEnvInt64("DAILY_LEECH_LIMIT", 0),
		DailyTaskLimit:            getEnvInt("DAILY_TASK_LIMIT", 0),
		StorageThreshold:          getEnvInt64("STORAGE_THRESHOLD", 1), // Default 1GB safety cushion
	}

	_ = os.MkdirAll(GlobalConfig.DownloadDir, 0755)
	_ = os.MkdirAll("Thumbnails", 0755)

	return &GlobalConfig, nil
}

func Get() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	return &GlobalConfig
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return defaultVal
	}
	return n
}

func getEnvInt64(key string, defaultVal int64) int64 {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if err != nil {
		return defaultVal
	}
	return n
}

func getEnvBool(key string, defaultVal bool) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if val == "" {
		return defaultVal
	}
	return val == "true" || val == "1" || val == "yes" || val == "t"
}

func parseIDList(val string) []int64 {
	var ids []int64
	parts := strings.Fields(val)
	for _, p := range parts {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func cleanDir(dir string) string {
	if !strings.HasSuffix(dir, "/") && !strings.HasSuffix(dir, "\\") {
		dir += "/"
	}
	return dir
}

func UpdateConfigVar(key, val string) error {
	configLock.Lock()
	defer configLock.Unlock()

	key = strings.ToUpper(strings.TrimSpace(key))
	switch key {
	case "STATUS_LIMIT":
		GlobalConfig.StatusLimit, _ = strconv.Atoi(val)
	case "STATUS_UPDATE_INTERVAL":
		GlobalConfig.StatusUpdateInterval, _ = strconv.Atoi(val)
	case "AUTO_DELETE_MESSAGE_DURATION":
		GlobalConfig.AutoDeleteMessageDuration, _ = strconv.Atoi(val)
	case "LEECH_SPLIT_SIZE":
		valInt, _ := strconv.ParseInt(val, 10, 64)
		if valInt > MaxSplitSize || valInt <= 0 {
			valInt = MaxSplitSize
		}
		GlobalConfig.LeechSplitSize = valInt
	case "AS_DOCUMENT":
		GlobalConfig.AsDocument = val == "true" || val == "1"
	case "EQUAL_SPLITS":
		GlobalConfig.EqualSplits = val == "true" || val == "1"
	case "MEDIA_GROUP":
		GlobalConfig.MediaGroup = val == "true" || val == "1"
	case "CAP_FONT":
		GlobalConfig.CapFont = val
	case "LEECH_FILENAME_PREFIX":
		GlobalConfig.LeechFilenamePrefix = val
	case "LEECH_FILENAME_SUFFIX":
		GlobalConfig.LeechFilenameSuffix = val
	case "LEECH_FILENAME_CAPTION":
		GlobalConfig.LeechFilenameCaption = val
	case "LEECH_FILENAME_REMNAME_AUTO":
		GlobalConfig.LeechFilenameRemnameAuto = val
	case "LEECH_FILENAME_REMNAME_REGEX":
		GlobalConfig.LeechFilenameRemnameRegex = val
	case "TORRENT_TIMEOUT":
		GlobalConfig.TorrentTimeout, _ = strconv.Atoi(val)
	case "SHORTENER":
		GlobalConfig.Shortener = val
	case "SHORTENER_API":
		GlobalConfig.ShortenerAPI = val
	case "SHORTENER_ENABLED":
		GlobalConfig.ShortenerEnabled = val == "true" || val == "1"
	case "TOKEN_TIMEOUT":
		GlobalConfig.TokenTimeout, _ = strconv.ParseInt(val, 10, 64)
	case "SCREENSHOTS_MODE":
		GlobalConfig.ScreenshotsMode = val == "true" || val == "1"
	case "ENABLE_FFMPEG_CMDS":
		GlobalConfig.EnableFFmpegCmds = val == "true" || val == "1"
	case "FFMPEG_ENCODING":
		GlobalConfig.FFmpegEncoding = val == "true" || val == "1"
	case "TMDB_ACCESS_TOKEN":
		GlobalConfig.TMDBAccessToken = val
	case "AUTO_THUMBNAIL":
		GlobalConfig.AutoThumbnail = val == "true" || val == "1"
	case "COVER_IMAGE":
		GlobalConfig.CoverImage = val
	case "DDL_API", "THUNDER_API":
		GlobalConfig.DDLAPI = val
	case "BASE_URL":
		GlobalConfig.BaseURL = val
	case "BASE_URL_PORT":
		GlobalConfig.BaseURLPort, _ = strconv.Atoi(val)
	case "QUEUE_ALL":
		GlobalConfig.QueueAll, _ = strconv.Atoi(val)
	case "QUEUE_DOWNLOAD":
		GlobalConfig.QueueDownload, _ = strconv.Atoi(val)
	case "QUEUE_UPLOAD":
		GlobalConfig.QueueUpload, _ = strconv.Atoi(val)
	}
	return nil
}

func GetConfigMap() map[string]string {
	configLock.RLock()
	defer configLock.RUnlock()

	return map[string]string{
		"STATUS_LIMIT":                 strconv.Itoa(GlobalConfig.StatusLimit),
		"STATUS_UPDATE_INTERVAL":       strconv.Itoa(GlobalConfig.StatusUpdateInterval),
		"AUTO_DELETE_MESSAGE_DURATION": strconv.Itoa(GlobalConfig.AutoDeleteMessageDuration),
		"LEECH_SPLIT_SIZE":             strconv.FormatInt(GlobalConfig.LeechSplitSize, 10),
		"AS_DOCUMENT":                  strconv.FormatBool(GlobalConfig.AsDocument),
		"EQUAL_SPLITS":                 strconv.FormatBool(GlobalConfig.EqualSplits),
		"MEDIA_GROUP":                  strconv.FormatBool(GlobalConfig.MediaGroup),
		"CAP_FONT":                     GlobalConfig.CapFont,
		"LEECH_FILENAME_PREFIX":        GlobalConfig.LeechFilenamePrefix,
		"LEECH_FILENAME_SUFFIX":        GlobalConfig.LeechFilenameSuffix,
		"LEECH_FILENAME_CAPTION":       GlobalConfig.LeechFilenameCaption,
		"LEECH_FILENAME_REMNAME_AUTO":  GlobalConfig.LeechFilenameRemnameAuto,
		"LEECH_FILENAME_REMNAME_REGEX": GlobalConfig.LeechFilenameRemnameRegex,
		"TORRENT_TIMEOUT":              strconv.Itoa(GlobalConfig.TorrentTimeout),
		"BASE_URL":                     GlobalConfig.BaseURL,
		"BASE_URL_PORT":                strconv.Itoa(GlobalConfig.BaseURLPort),
		"SHORTENER":                    GlobalConfig.Shortener,
		"SHORTENER_API":                GlobalConfig.ShortenerAPI,
		"SHORTENER_ENABLED":            strconv.FormatBool(GlobalConfig.ShortenerEnabled),
		"TOKEN_TIMEOUT":                strconv.FormatInt(GlobalConfig.TokenTimeout, 10),
		"SCREENSHOTS_MODE":             strconv.FormatBool(GlobalConfig.ScreenshotsMode),
		"ENABLE_FFMPEG_CMDS":           strconv.FormatBool(GlobalConfig.EnableFFmpegCmds),
		"FFMPEG_ENCODING":              strconv.FormatBool(GlobalConfig.FFmpegEncoding),
		"TMDB_ACCESS_TOKEN":            GlobalConfig.TMDBAccessToken,
		"AUTO_THUMBNAIL":               strconv.FormatBool(GlobalConfig.AutoThumbnail),
		"COVER_IMAGE":                  GlobalConfig.CoverImage,
		"DDL_API":                      GlobalConfig.DDLAPI,
		"QUEUE_ALL":                    strconv.Itoa(GlobalConfig.QueueAll),
		"QUEUE_DOWNLOAD":               strconv.Itoa(GlobalConfig.QueueDownload),
		"QUEUE_UPLOAD":                 strconv.Itoa(GlobalConfig.QueueUpload),
	}
}

