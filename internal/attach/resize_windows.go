package attach

import (
	"os"
	"time"
)

// Windows has no SIGWINCH; polling is bounded by the attachment lifetime.
func watchResize() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case ch <- os.Interrupt:
				default:
				}
			}
		}
	}()
	return ch, func() { close(stop) }
}
