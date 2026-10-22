package telegram

import (
	"strings"

	"azml-go/config"
)

type Commands struct {
	Start       []string
	Leech       []string
	Ytdl        []string
	Status      []string
	Cancel      []string
	CancelAll   []string
	Authorize   []string
	UnAuthorize []string
	AddSudo     []string
	RmSudo      []string
	AddPremium  []string
	RmPremium   []string
	Premium     []string
	Blacklist   []string
	RmBlacklist []string
	Ping        []string
	Stats       []string
	Restart     []string
	Help        []string
	Log         []string
	UserSetting []string
	BotSetting  []string
	Speedtest   []string
	MediaInfo   []string
	Broadcast   []string
	Imdb        []string
	Anime       []string
}

var BotCommands Commands

func InitCommands() {
	suffix := config.Get().CmdSuffix

	BotCommands = Commands{
		Start: []string{
			"start" + suffix,
			"start",
		},
		Leech: []string{
			"leech" + suffix,
			"l" + suffix,
			"zipleech" + suffix,
			"zl" + suffix,
			"unzipleech" + suffix,
			"uzl" + suffix,
		},
		Ytdl: []string{
			"ytdl" + suffix,
			"y" + suffix,
			"ytdlleech" + suffix,
			"yl" + suffix,
			"ytdlzip" + suffix,
			"yz" + suffix,
			"ytdlzipleech" + suffix,
			"yzl" + suffix,
		},
		Status: []string{
			"status" + suffix,
			"s" + suffix,
			"statusall" + suffix,
		},
		Cancel: []string{
			"cancel" + suffix,
			"c" + suffix,
			"del" + suffix,
		},
		CancelAll: []string{
			"cancelall" + suffix,
			"ca" + suffix,
			"cancelallbot" + suffix,
		},
		Authorize: []string{
			"authorize" + suffix,
			"a" + suffix,
		},
		UnAuthorize: []string{
			"unauthorize" + suffix,
			"ua" + suffix,
		},
		AddSudo: []string{
			"addsudo" + suffix,
			"as" + suffix,
		},
		RmSudo: []string{
			"rmsudo" + suffix,
			"rs" + suffix,
		},
		AddPremium: []string{
			"addpremium" + suffix,
			"addpremuim" + suffix,
			"ap" + suffix,
		},
		RmPremium: []string{
			"rmpremium" + suffix,
			"rmpremuim" + suffix,
			"removepremium" + suffix,
			"removepremuim" + suffix,
			"rp" + suffix,
		},
		Premium: []string{
			"premium" + suffix,
			"premuim" + suffix,
		},
		Blacklist: []string{
			"blacklist" + suffix,
			"bl" + suffix,
		},
		RmBlacklist: []string{
			"rmblacklist" + suffix,
			"rbl" + suffix,
		},
		Ping: []string{
			"ping" + suffix,
			"p" + suffix,
		},
		Stats: []string{
			"stats" + suffix,
			"st" + suffix,
		},
		Restart: []string{
			"restart" + suffix,
			"r" + suffix,
			"restartall" + suffix,
		},
		Help: []string{
			"help" + suffix,
			"h" + suffix,
		},
		Log: []string{
			"log" + suffix,
		},
		UserSetting: []string{
			"usetting" + suffix,
			"us" + suffix,
			"usersetting" + suffix,
			"usersettings" + suffix,
		},
		BotSetting: []string{
			"bsetting" + suffix,
			"bs" + suffix,
			"botsetting" + suffix,
			"botsettings" + suffix,
		},
		Speedtest: []string{
			"speedtest" + suffix,
			"sp" + suffix,
			"speed" + suffix,
		},
		MediaInfo: []string{
			"mediainfo" + suffix,
			"mi" + suffix,
		},
		Broadcast: []string{
			"broadcast" + suffix,
			"bc" + suffix,
		},
		Imdb: []string{
			"imdb" + suffix,
		},
		Anime: []string{
			"anime" + suffix,
			"anilist" + suffix,
			"al" + suffix,
		},
	}
}

func MatchCommand(text string, targets []string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false
	}
	cmd := strings.TrimPrefix(parts[0], "/")
	if atIdx := strings.Index(cmd, "@"); atIdx != -1 {
		cmd = cmd[:atIdx]
	}
	cmd = strings.ToLower(cmd)
	for _, t := range targets {
		if cmd == strings.ToLower(t) {
			return true
		}
	}
	return false
}

func MatchSingleCommand(text, target string) bool {
	return MatchCommand(text, []string{target})
}

// MatchCancelCommand checks if text is a cancel command (/cancel, /cancel_GID, /del, /del_GID) and extracts the GID
func MatchCancelCommand(text string) (bool, string) {
	if !strings.HasPrefix(text, "/") {
		return false, ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false, ""
	}
	cmdToken := strings.TrimPrefix(fields[0], "/")
	if atIdx := strings.Index(cmdToken, "@"); atIdx != -1 {
		cmdToken = cmdToken[:atIdx]
	}

	// Do not match cancelall
	if strings.HasPrefix(strings.ToLower(cmdToken), "cancelall") {
		return false, ""
	}

	// Check underscore format: /cancel_GID or /del_GID
	if strings.Contains(cmdToken, "_") {
		parts := strings.SplitN(cmdToken, "_", 2)
		cmdBase := strings.ToLower(parts[0])
		for _, c := range BotCommands.Cancel {
			if cmdBase == strings.ToLower(c) {
				return true, parts[1]
			}
		}
	}

	// Check space format: /cancel GID or /del GID
	cmdBase := strings.ToLower(cmdToken)
	for _, c := range BotCommands.Cancel {
		if cmdBase == strings.ToLower(c) {
			gid := ""
			if len(fields) > 1 {
				gid = fields[1]
			}
			return true, gid
		}
	}

	return false, ""
}
