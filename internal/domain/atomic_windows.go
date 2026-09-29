//go:build windows

package domain

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// replaceFile atomically moves source over destination. Plain rename on
// Windows goes through MoveFile, which refuses to overwrite an existing
// file, so MoveFileEx with MOVEFILE_REPLACE_EXISTING is used instead, and
// MOVEFILE_WRITE_THROUGH flushes to disk before returning.
func replaceFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// A virus scanner or indexer can hold either file open for a moment and
	// make the replace fail with a sharing violation or access denied; try a
	// few times before giving up.
	var last error
	for attempt := 0; attempt < replaceAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(replaceRetryDelay)
		}
		last = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if last == nil || !(errors.Is(last, windows.ERROR_ACCESS_DENIED) || errors.Is(last, windows.ERROR_SHARING_VIOLATION)) {
			return last
		}
	}
	return last
}

const (
	replaceAttempts   = 5
	replaceRetryDelay = 50 * time.Millisecond
)
