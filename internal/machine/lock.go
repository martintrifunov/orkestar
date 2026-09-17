package machine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// lockTimeout bounds how long a writer waits for another one. Catalog
	// writes are milliseconds, so waiting longer means something is stuck.
	lockTimeout = 5 * time.Second
	// lockStaleAfter is when a lock directory left by a crashed writer may
	// be cleared by the next one.
	lockStaleAfter = 30 * time.Second
)

// AcquireCatalogLock holds an exclusive, cross-process lock for the catalog at
// path while the caller reads, mutates and saves it. Save writes atomically,
// but two processes loading the same file and saving in turn would still lose
// one update; the lock makes the whole read-modify-write one critical
// section. It is a directory, not a file lock, so it works the same on every
// platform Orkestar ships on. A directory left by a crashed holder is cleared
// once stale; otherwise a kill -9 would wedge every later write.
func AcquireCatalogLock(path string) (release func(), err error) {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create machine catalog directory: %w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		if err := os.Mkdir(lock, 0o700); err == nil {
			return func() { _ = os.RemoveAll(lock) }, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock machine catalog: %w", err)
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
			_ = os.RemoveAll(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("another orkestar command is updating the machine catalog; try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
