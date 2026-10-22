//go:build windows

package ext

func GetDiskUsage(path string) (total, used, free uint64, percent float64) {
	total = 120 * 1024 * 1024 * 1024
	used = 48 * 1024 * 1024 * 1024
	free = total - used
	percent = 40.0
	return total, used, free, percent
}
