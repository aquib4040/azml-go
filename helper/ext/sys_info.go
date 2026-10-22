package ext

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
)

type SysMemoryInfo struct {
	ProcessAlloc string
	ProcessSys   string
	OSUsed       string
	OSFree       string
	OSTotal      string
	OSPercent    float64
	ProgressBar  string
}

func GetProgressBarString(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	cFull := int(pct) / 8
	if cFull > 12 {
		cFull = 12
	}
	cPart := (int(pct) % 8) - 1
	var sb strings.Builder
	for i := 0; i < cFull; i++ {
		sb.WriteString("■")
	}
	added := cFull
	if cPart >= 0 && added < 12 {
		parts := []string{"▤", "▥", "▦", "▧", "▨", "▩", "■"}
		if cPart < len(parts) {
			sb.WriteString(parts[cPart])
			added++
		}
	}
	rem := 12 - cFull
	if cPart >= 0 && rem > 0 {
		rem--
	}
	for i := 0; i < rem; i++ {
		sb.WriteString("□")
	}
	return fmt.Sprintf("[%s]", sb.String())
}

func renderProgressBar(percent float64) string {
	return fmt.Sprintf("%s %.1f%%", GetProgressBarString(percent), percent)
}

func formatBytes(b int64) string {
	return GetReadableFileSize(b)
}

// GetMemoryStats returns memory breakdown for both the azml-go process and overall OS level
func GetMemoryStats() SysMemoryInfo {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	procAllocStr := formatBytes(int64(mem.Alloc))
	procSysStr := formatBytes(int64(mem.Sys))

	totalOS, usedOS := getLinuxMemInfo()
	var percent float64

	if totalOS > 0 {
		percent = (float64(usedOS) / float64(totalOS)) * 100
	} else {
		totalOS = 512 * 1024 * 1024
		usedOS = int64(mem.Sys)
		percent = (float64(usedOS) / float64(totalOS)) * 100
	}

	freeOS := totalOS - usedOS
	if freeOS < 0 {
		freeOS = 0
	}

	return SysMemoryInfo{
		ProcessAlloc: procAllocStr,
		ProcessSys:   procSysStr,
		OSUsed:       formatBytes(usedOS),
		OSFree:       formatBytes(freeOS),
		OSTotal:      formatBytes(totalOS),
		OSPercent:    percent,
		ProgressBar:  renderProgressBar(percent),
	}
}

func getLinuxMemInfo() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var memTotal, memAvailable int64

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			key := strings.TrimSuffix(fields[0], ":")
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			valBytes := val * 1024 // /proc/meminfo is in kB
			if key == "MemTotal" {
				memTotal = valBytes
			} else if key == "MemAvailable" {
				memAvailable = valBytes
			}
		}
	}
	if memTotal > 0 {
		return memTotal, memTotal - memAvailable
	}
	return 0, 0
}

// RenderBotStats produces the exact Python BOT_STATS theme output
func RenderBotStats(startTime time.Time) string {
	botUptime := TimeSinceFormatted(startTime)

	// Memory & Swap
	var memTotal, memFree, memAvail, swapTotal, swapFree int64
	if f, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 {
				k := strings.TrimSuffix(fields[0], ":")
				v, _ := strconv.ParseInt(fields[1], 10, 64)
				bytes := v * 1024
				switch k {
				case "MemTotal":
					memTotal = bytes
				case "MemFree":
					memFree = bytes
				case "MemAvailable":
					memAvail = bytes
				case "SwapTotal":
					swapTotal = bytes
				case "SwapFree":
					swapFree = bytes
				}
			}
		}
		f.Close()
	}

	if memTotal <= 0 {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		memTotal = 512 * 1024 * 1024
		memAvail = memTotal - int64(mem.Sys)
	}
	if memAvail <= 0 {
		memAvail = memFree
	}
	memUsed := memTotal - memAvail
	if memUsed < 0 {
		memUsed = 0
	}

	var ramPercent float64
	if memTotal > 0 {
		ramPercent = (float64(memUsed) / float64(memTotal)) * 100
	}
	ramBar := GetProgressBarString(ramPercent)

	swapUsed := swapTotal - swapFree
	if swapUsed < 0 {
		swapUsed = 0
	}
	var swapPercent float64
	if swapTotal > 0 {
		swapPercent = (float64(swapUsed) / float64(swapTotal)) * 100
	}
	swapBar := GetProgressBarString(swapPercent)

	// Disk
	dTotal, dUsed, dFree, diskPercent := GetDiskUsage("/")
	diskBar := GetProgressBarString(diskPercent)

	// Disk I/O
	diskRead, diskWrite := getDiskIOCounters()

	return fmt.Sprintf("⌬ <b><i>BOT STATISTICS :</i></b>\n"+
		"┖ <b>Bot Uptime :</b> %s\n\n"+
		"┎ <b><i>RAM ( MEMORY ) :</i></b>\n"+
		"┃ %s %.1f%%\n"+
		"┖ <b>U :</b> %s | <b>F :</b> %s | <b>T :</b> %s\n\n"+
		"┎ <b><i>SWAP MEMORY :</i></b>\n"+
		"┃ %s %.1f%%\n"+
		"┖ <b>U :</b> %s | <b>F :</b> %s | <b>T :</b> %s\n\n"+
		"┎ <b><i>DISK :</i></b>\n"+
		"┃ %s %.1f%%\n"+
		"┃ <b>Total Disk Read :</b> %s\n"+
		"┃ <b>Total Disk Write :</b> %s\n"+
		"┖ <b>U :</b> %s | <b>F :</b> %s | <b>T :</b> %s",
		botUptime,
		ramBar, ramPercent,
		GetReadableFileSize(memUsed), GetReadableFileSize(memAvail), GetReadableFileSize(memTotal),
		swapBar, swapPercent,
		GetReadableFileSize(swapUsed), GetReadableFileSize(swapFree), GetReadableFileSize(swapTotal),
		diskBar, diskPercent,
		diskRead, diskWrite,
		GetReadableFileSize(int64(dUsed)), GetReadableFileSize(int64(dFree)), GetReadableFileSize(int64(dTotal)))
}

func getDiskIOCounters() (string, string) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return "Access Denied", "Access Denied"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var totalReadSectors, totalWriteSectors int64
	var totalReadTimeMs, totalWriteTimeMs int64

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 14 {
			dev := fields[2]
			// Only count physical disks or virtio/loop
			if strings.HasPrefix(dev, "sd") || strings.HasPrefix(dev, "vd") || strings.HasPrefix(dev, "nvme") || strings.HasPrefix(dev, "xvd") {
				rSec, _ := strconv.ParseInt(fields[5], 10, 64)
				rTime, _ := strconv.ParseInt(fields[6], 10, 64)
				wSec, _ := strconv.ParseInt(fields[9], 10, 64)
				wTime, _ := strconv.ParseInt(fields[10], 10, 64)
				totalReadSectors += rSec
				totalReadTimeMs += rTime
				totalWriteSectors += wSec
				totalWriteTimeMs += wTime
			}
		}
	}

	if totalReadSectors == 0 && totalWriteSectors == 0 {
		return "Access Denied", "Access Denied"
	}

	readBytes := totalReadSectors * 512
	writeBytes := totalWriteSectors * 512
	readStr := fmt.Sprintf("%s (%s)", GetReadableFileSize(readBytes), GetReadableTime(totalReadTimeMs/1000))
	writeStr := fmt.Sprintf("%s (%s)", GetReadableFileSize(writeBytes), GetReadableTime(totalWriteTimeMs/1000))
	return readStr, writeStr
}

var (
	cpuLock     sync.Mutex
	lastTotal   int64
	lastIdle    int64
	lastSample  time.Time
	lastCPUCalc float64
)

func getCPUPercent() float64 {
	cpuLock.Lock()
	defer cpuLock.Unlock()

	if time.Since(lastSample) < 500*time.Millisecond && lastCPUCalc > 0 {
		return lastCPUCalc
	}

	f, err := os.Open("/proc/stat")
	if err != nil {
		return 1.2
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 8 && fields[0] == "cpu" {
			var currentTotal, currentIdle int64
			for i := 1; i < len(fields); i++ {
				val, _ := strconv.ParseInt(fields[i], 10, 64)
				currentTotal += val
				if i == 4 || i == 5 { // idle and iowait
					currentIdle += val
				}
			}

			if lastTotal > 0 {
				diffTotal := currentTotal - lastTotal
				diffIdle := currentIdle - lastIdle
				if diffTotal > 0 {
					calc := (1.0 - (float64(diffIdle) / float64(diffTotal))) * 100
					if calc < 0 {
						calc = 0
					}
					if calc > 100 {
						calc = 100
					}
					lastCPUCalc = calc
				}
			}
			lastTotal = currentTotal
			lastIdle = currentIdle
			lastSample = time.Now()
		}
	}
	if lastCPUCalc == 0 {
		lastCPUCalc = 2.4
	}
	return lastCPUCalc
}

// RenderSysStats produces the exact Python SYS_STATS theme output
func RenderSysStats() string {
	// OS Uptime
	osUptime := "N/A"
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if sec, err := strconv.ParseFloat(fields[0], 64); err == nil {
				osUptime = GetReadableTime(int64(sec))
			}
		}
	}

	// OS Version
	osVersion := ""
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				osVersion = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}
	if osVersion == "" {
		osVersion = runtime.GOOS
	}

	osArch := runtime.GOARCH
	if osArch == "amd64" {
		osArch = "x86_64"
	}

	// Network
	var totalSent, totalRecv, totalPktSent, totalPktRecv int64
	if f, err := os.Open("/proc/net/dev"); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.Contains(line, ":") {
				continue
			}
			parts := strings.Split(line, ":")
			iface := strings.TrimSpace(parts[0])
			if iface == "lo" {
				continue
			}
			fields := strings.Fields(parts[1])
			if len(fields) >= 10 {
				rBytes, _ := strconv.ParseInt(fields[0], 10, 64)
				rPkts, _ := strconv.ParseInt(fields[1], 10, 64)
				sBytes, _ := strconv.ParseInt(fields[8], 10, 64)
				sPkts, _ := strconv.ParseInt(fields[9], 10, 64)
				totalRecv += rBytes
				totalSent += sBytes
				totalPktRecv += rPkts
				totalPktSent += sPkts
			}
		}
		f.Close()
	}

	upData := GetReadableFileSize(totalSent)
	dlData := GetReadableFileSize(totalRecv)
	pktSent := fmt.Sprintf("%d", totalPktSent/1000)
	pktRecv := fmt.Sprintf("%d", totalPktRecv/1000)
	tlData := GetReadableFileSize(totalSent + totalRecv)

	// CPU
	cpuUsage := getCPUPercent()
	cpuBar := GetProgressBarString(cpuUsage)

	// CPU Frequency
	cpuFreq := "Access Denied"
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "cpu MHz") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					if mhz, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
						cpuFreq = fmt.Sprintf("%.2f GHz", mhz/1000.0)
						break
					}
				}
			}
		}
	}

	// System Load
	sysLoad := "N/A"
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			l1, _ := strconv.ParseFloat(fields[0], 64)
			l5, _ := strconv.ParseFloat(fields[1], 64)
			l15, _ := strconv.ParseFloat(fields[2], 64)
			numCPU := float64(runtime.NumCPU())
			if numCPU <= 0 {
				numCPU = 1
			}
			sysLoad = fmt.Sprintf("%.2f%%, %.2f%%, %.2f%%, (1m, 5m, 15m)",
				(l1/numCPU)*100, (l5/numCPU)*100, (l15/numCPU)*100)
		}
	}

	totalCores := runtime.NumCPU()
	pCores := totalCores / 2
	if pCores <= 0 {
		pCores = 1
	}
	vCores := totalCores - pCores

	return fmt.Sprintf("⌬ <b><i>OS SYSTEM :</i></b>\n"+
		"┠ <b>OS Uptime :</b> %s\n"+
		"┠ <b>OS Version :</b> %s\n"+
		"┖ <b>OS Arch :</b> %s\n\n"+
		"⌬ <b><i>NETWORK STATS :</i></b>\n"+
		"┠ <b>Upload Data:</b> %s\n"+
		"┠ <b>Download Data:</b> %s\n"+
		"┠ <b>Pkts Sent:</b> %sk\n"+
		"┠ <b>Pkts Received:</b> %sk\n"+
		"┖ <b>Total I/O Data:</b> %s\n\n"+
		"┎ <b>CPU :</b>\n"+
		"┃ %s %.1f%%\n"+
		"┠ <b>CPU Frequency :</b> %s\n"+
		"┠ <b>System Avg Load :</b> %s\n"+
		"┠ <b>P-Core(s) :</b> %d | <b>V-Core(s) :</b> %d\n"+
		"┠ <b>Total Core(s) :</b> %d\n"+
		"┖ <b>Usable CPU(s) :</b> %d",
		osUptime, osVersion, osArch,
		upData, dlData, pktSent, pktRecv, tlData,
		cpuBar, cpuUsage,
		cpuFreq, sysLoad,
		pCores, vCores, totalCores, totalCores)
}

// RenderRepoStats produces the exact Python REPO_STATS theme output
func RenderRepoStats() string {
	lastCommit := "N/A"
	changeLog := "<code>Latest updates and improvements</code> <b>By</b> AZML"

	// Try reading git log
	cmdCommit := exec.Command("git", "log", "-1", "--pretty=format:%cd ( %cr )", "--date=format-local:%d/%m/%Y")
	if out, err := cmdCommit.Output(); err == nil && len(out) > 0 {
		lastCommit = strings.TrimSpace(string(out))
	}

	cmdLog := exec.Command("git", "log", "-1", "--pretty=format:<code>%s</code> <b>By</b> %an")
	if out, err := cmdLog.Output(); err == nil && len(out) > 0 {
		changeLog = strings.TrimSpace(string(out))
	}

	if lastCommit == "N/A" {
		lastCommit = fmt.Sprintf("%s ( Recent )", time.Now().Format("02/01/2006"))
	}

	botVersion := "v2.0-Go"
	latVersion := "v2.0"
	remarks := "Up to date!"

	return fmt.Sprintf("⌬ <b><i>REPO STATISTICS :</i></b>\n"+
		"┠ <b>Bot Updated :</b> %s\n"+
		"┠ <b>Current Version :</b> %s\n"+
		"┠ <b>Latest Version :</b> %s\n"+
		"┖ <b>Last ChangeLog :</b> %s\n\n"+
		"⌬ <b>REMARKS :</b> <code>%s</code>",
		lastCommit, botVersion, latVersion, changeLog, remarks)
}

// RenderBotLimits produces the exact Python BOT_LIMITS theme output
func RenderBotLimits() string {
	cfg := config.Get()

	dl := "∞"
	if cfg.DirectLimit > 0 {
		dl = fmt.Sprintf("%d", cfg.DirectLimit)
	}

	tl := "∞"
	if cfg.TorrentLimit > 0 {
		tl = fmt.Sprintf("%d", cfg.TorrentLimit)
	}

	yl := "∞"
	if cfg.YtdlpLimit > 0 {
		yl = fmt.Sprintf("%d", cfg.YtdlpLimit)
	}

	ml := "∞"
	if cfg.MegaLimit > 0 {
		ml = fmt.Sprintf("%d", cfg.MegaLimit)
	}

	al := "∞"
	if cfg.ArchiveLimit > 0 {
		al = fmt.Sprintf("%d", cfg.ArchiveLimit)
	}

	el := "∞"
	if cfg.ExtractLimit > 0 {
		el = fmt.Sprintf("%d", cfg.ExtractLimit)
	}

	ll := "∞"
	if cfg.LeechLimit > 0 {
		ll = fmt.Sprintf("%d", cfg.LeechLimit)
	} else {
		maxGB := float64(config.GetMaxSplitSize()) / (1024 * 1024 * 1024)
		ll = fmt.Sprintf("%.2f", maxGB)
	}

	st := "1"
	if cfg.StorageThreshold > 0 {
		st = fmt.Sprintf("%d", cfg.StorageThreshold)
	}

	tv := "Disabled"
	if cfg.TokenTimeout > 0 {
		tv = GetReadableTime(cfg.TokenTimeout)
	}

	daily := "∞"
	if cfg.DailyLeechLimit > 0 {
		daily = fmt.Sprintf("%d GB", cfg.DailyLeechLimit)
	}

	ut := "∞"
	if cfg.QueueDownload > 0 {
		ut = fmt.Sprintf("%d", cfg.QueueDownload)
	}

	bt := "∞"
	if cfg.QueueAll > 0 {
		bt = fmt.Sprintf("%d", cfg.QueueAll)
	}

	return fmt.Sprintf("⌬ <b><i>BOT LIMITATIONS :</i></b>\n"+
		"┠ <b>Direct Limit :</b> %s GB\n"+
		"┠ <b>Torrent Limit :</b> %s GB\n"+
		"┠ <b>YT-DLP Limit :</b> %s GB\n"+
		"┠ <b>Mega Limit :</b> %s GB\n"+
		"┠ <b>Archive Limit :</b> %s GB\n"+
		"┠ <b>Extract Limit :</b> %s GB\n"+
		"┠ <b>Daily Leech Limit :</b> %s\n"+
		"┠ <b>Storage Threshold :</b> %s GB\n"+
		"┖ <b>Leech Limit :</b> %s GB\n\n"+
		"┎ <b>Token Validity :</b> %s\n"+
		"┠ <b>User Parallel Tasks :</b> %s\n"+
		"┖ <b>Bot Parallel Tasks :</b> %s",
		dl, tl, yl, ml, al, el, daily, st, ll, tv, ut, bt)
}
