//go:build !windows

package slicer

// osPlatform reports an unsupported platform: Creality Print is only driven on
// Windows (the installed product this package targets).
func osPlatform() Platform {
	return Platform{Supported: false, FS: osFS{}}
}
