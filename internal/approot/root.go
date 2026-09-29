// Package approot locates the Encore app root by walking up to encore.app.
package approot

import (
	"fmt"
	"os"
	"path/filepath"
)

// Root returns the directory that contains encore.app.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "encore.app")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("encore.app not found from %s", dir)
		}
		dir = parent
	}
}
