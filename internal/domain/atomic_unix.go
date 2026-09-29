//go:build !windows

package domain

import "os"

// replaceFile atomically moves source over destination.
func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
