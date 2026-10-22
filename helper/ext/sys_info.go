package ext

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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
		// Fallback estimation
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


func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.2f %s", float64(b)/float64(div), units[exp])
}

func renderProgressBar(percent float64) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	totalBlocks := 10
	filledBlocks := int((percent / 100.0) * float64(totalBlocks))
	if filledBlocks > totalBlocks {
		filledBlocks = totalBlocks
	}
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < filledBlocks; i++ {
		sb.WriteString("■")
	}
	for i := filledBlocks; i < totalBlocks; i++ {
		sb.WriteString("□")
	}
	sb.WriteString(fmt.Sprintf("] %.1f%%", percent))
	return sb.String()
}
