package gcodeinfo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// MaxLineBytes is the longest line the reader accepts. Real G-code lines are
// far shorter (the config block lines stay below 1 MiB); a file with a longer
// one is not G-code (a binary or a wrong file) and reading it whole would only
// blow up memory. This is an anti-break guard, not a product limit.
const MaxLineBytes = 64 << 20

// ErrLineTooLong is returned when a line passes MaxLineBytes.
var ErrLineTooLong = fmt.Errorf("gcodeinfo: line longer than %d MiB, not a G-code file", MaxLineBytes>>20)

// lineReader reads a file line by line without allocating per line and
// reports the byte offset at which each line starts. Lines of any length are
// supported (the config block has very long ones); the returned slice is only
// valid until the next call.
type lineReader struct {
	r    *bufio.Reader
	off  int64 // offset of the next line to be read
	long []byte
}

func newLineReader(r io.Reader, offset int64) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 256<<10), off: offset}
}

// next returns the next line without its line terminator (LF or CRLF), the
// offset of its first byte and the number of bytes it occupies including the
// terminator. It returns io.EOF after the last line.
func (l *lineReader) next() (line []byte, start int64, size int, err error) {
	start = l.off
	chunk, err := l.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		l.long = append(l.long[:0], chunk...)
		for errors.Is(err, bufio.ErrBufferFull) {
			chunk, err = l.r.ReadSlice('\n')
			l.long = append(l.long, chunk...)
			if len(l.long) > MaxLineBytes {
				return nil, start, 0, ErrLineTooLong
			}
		}
		chunk = l.long
	}
	if err != nil && err != io.EOF {
		return nil, start, 0, err
	}
	if len(chunk) == 0 && err == io.EOF {
		return nil, start, 0, io.EOF
	}
	size = len(chunk)
	l.off += int64(size)
	line = bytes.TrimSuffix(chunk, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	return line, start, size, nil
}
