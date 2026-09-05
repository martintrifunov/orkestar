//go:build !windows

package attach

import (
	"os"
	"os/signal"
	"syscall"
)

func watchResize() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch, func() { signal.Stop(ch) }
}
