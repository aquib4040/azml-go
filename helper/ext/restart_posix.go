//go:build !windows

package ext

import "syscall"

// RestartProcess replaces the current process image on POSIX/Linux systems using syscall.Exec
func RestartProcess(exePath string, args []string, env []string) error {
	return syscall.Exec(exePath, args, env)
}
