//go:build !windows

package ext

import "syscall"

// GetDiskUsage returns total, used, free bytes and usage percent for unix/linux systems
func GetDiskUsage(path string) (total, used, free uint64, percent float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, 0, 0
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bavail * uint64(stat.Bsize)
	used = total - free
	if total > 0 {
		percent = (float64(used) / float64(total)) * 100
	}
	return total, used, free, percent
}
