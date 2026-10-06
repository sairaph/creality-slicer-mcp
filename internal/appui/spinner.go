package appui

import (
	"os"
	"runtime"
	"strings"
)

var (
	brailleFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	asciiFrames   = []string{"-", "\\", "|", "/"}
)

// Frames are the spinner frames: braille on a terminal that shows UTF-8,
// else the four ASCII ones.
func Frames(unicode bool) []string {
	if unicode {
		return brailleFrames
	}
	return asciiFrames
}

// SpinnerFrame returns frame n of the spinner.
func SpinnerFrame(unicode bool, n int) string {
	f := Frames(unicode)
	if n < 0 {
		n = 0
	}
	return f[n%len(f)]
}

// UnicodeTerminal reports whether the terminal shows UTF-8: always on Windows,
// else when the locale says so.
func UnicodeTerminal() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(os.Getenv(name))
		if v == "" {
			continue
		}
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	return false
}
