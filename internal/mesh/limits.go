package mesh

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxFileBytes is the largest model file, and the largest inflated 3MF member,
// the readers take into memory (an anti-break guard: a file of many gigabytes
// or a zip bomb would otherwise end the server by exhausting memory).
const MaxFileBytes int64 = 2 << 30

// maxFileBytes is MaxFileBytes; tests lower it.
var maxFileBytes = MaxFileBytes

// ErrTooLarge means a model file or a member of a 3MF is over MaxFileBytes.
var ErrTooLarge = errors.New("mesh: the model file is too large to read")

// readFileLimited reads a whole file unless it is over MaxFileBytes.
func readFileLimited(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("%w: %s has %d bytes, the limit is %d", ErrTooLarge, path, info.Size(), maxFileBytes)
	}
	return os.ReadFile(path)
}

// readAllLimited reads r up to MaxFileBytes; more is ErrTooLarge. The declared
// size of a zip member is not trusted, the bytes actually produced are counted.
func readAllLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxFileBytes {
		return nil, fmt.Errorf("%w: a member is over %d bytes", ErrTooLarge, maxFileBytes)
	}
	return data, nil
}
