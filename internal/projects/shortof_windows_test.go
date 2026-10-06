//go:build windows

package projects

import "syscall"

// shortOf is the 8.3 short form of a path, or "" when there is none.
func shortOf(p string) string {
	in, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
