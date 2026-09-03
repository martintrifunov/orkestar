package tui

import (
	"log"
	"os"
	"sync"
)

// debugf writes a diagnostic line to /tmp/orkestar-tui-debug.log when
// ORKESTAR_TUI_DEBUG is set. It exists purely to turn "why isn't input
// reaching the embedded pane" from a guess into something inspectable
// after the fact, without corrupting the TUI's own screen output.
var (
	debugOnce   sync.Once
	debugLogger *log.Logger
)

func debugf(format string, args ...any) {
	if os.Getenv("ORKESTAR_TUI_DEBUG") == "" {
		return
	}
	debugOnce.Do(func() {
		file, err := os.OpenFile("/tmp/orkestar-tui-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		debugLogger = log.New(file, "", log.LstdFlags|log.Lmicroseconds)
	})
	if debugLogger != nil {
		debugLogger.Printf(format, args...)
	}
}
