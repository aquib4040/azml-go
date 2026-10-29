//go:build windows

package ext

import (
	"os"
	"os/exec"
)

// RestartProcess fallback stub
func RestartProcess(exePath string, args []string, env []string) error {
	cmd := exec.Command(exePath, args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
