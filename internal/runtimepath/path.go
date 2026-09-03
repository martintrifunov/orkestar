package runtimepath

import (
	"fmt"
	"os"
	"path/filepath"
)

const runtimeDirectoryEnvironment = "ORKESTAR_RUNTIME_DIR"

type Paths struct {
	Directory string
	Socket    string
	Log       string
}

func Resolve() (Paths, error) {
	directory := os.Getenv(runtimeDirectoryEnvironment)
	if directory == "" {
		cacheDirectory, err := os.UserCacheDir()
		if err != nil {
			return Paths{}, fmt.Errorf("find user cache directory: %w", err)
		}
		directory = filepath.Join(cacheDirectory, "orkestar")
	}

	directory, err := filepath.Abs(directory)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve runtime directory: %w", err)
	}

	return Paths{
		Directory: directory,
		Socket:    filepath.Join(directory, "orkestar.sock"),
		Log:       filepath.Join(directory, "daemon.log"),
	}, nil
}

func Ensure(paths Paths) error {
	if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	return nil
}
