//go:build windows

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
		return true // нечем проверить — не блокируем
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Windows: -n количество, -w таймаут в мс
	ms := int(timeout.Milliseconds())
	if ms < 100 {
		ms = 100
	}
	return exec.CommandContext(ctx, "ping", "-n", "1", "-w", itoa(ms), host).Run() == nil
}