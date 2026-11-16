//go:build !windows

package control

import (
	"context"
	"os/exec"
	"time"
)

func pingHost(host string, timeout time.Duration) bool {
	if host == "" {
		return false
	}
	if _, err := exec.LookPath("ping"); err != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, "ping", "-c", "1", "-W", "1", host).Run() == nil
}