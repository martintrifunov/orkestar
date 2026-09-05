package ipc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Retain the filesystem locator for metadata and hooks, deriving a private pipe.
func pipeName(path string) string {
	absolute, _ := filepath.Abs(path)
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
	return fmt.Sprintf(`\\.\pipe\orkestar-%x`, sum[:16])
}
func Dial(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return winio.DialPipeContext(ctx, pipeName(path))
}
func Listen(path string) (net.Listener, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(pipeName(path), &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")",
	})
}

// A live pipe means another daemon owns this runtime path. Unlike a Unix
// socket, a pipe leaves nothing stale behind, so there is nothing to remove.
func PrepareListener(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, pipeName(path))
	if err != nil {
		return nil
	}
	conn.Close()
	return ErrAlreadyRunning
}
func RestrictListener(path string) error { return nil }
func RemoveListener(path string) error   { return nil }
