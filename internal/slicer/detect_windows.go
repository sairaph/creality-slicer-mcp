//go:build windows

package slicer

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func osPlatform() Platform {
	return Platform{
		Supported:   true,
		Registry:    winRegistry{},
		FileVersion: winFileVersion{},
		Processes:   winProcesses{},
		FS:          osFS{},
		Getenv:      os.Getenv,
	}
}

// winRegistry reads the uninstall keys the installer writes.
type winRegistry struct{}

func (winRegistry) UninstallEntries() ([]UninstallEntry, error) {
	roots := []struct {
		key  registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	var out []UninstallEntry
	for _, root := range roots {
		k, err := registry.OpenKey(root.key, root.path, registry.ENUMERATE_SUB_KEYS|registry.READ)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, name := range names {
			sub, err := registry.OpenKey(root.key, root.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			e := UninstallEntry{Key: name}
			e.DisplayName, _, _ = sub.GetStringValue("DisplayName")
			if hasProductPrefix(e.DisplayName) {
				e.DisplayVersion, _, _ = sub.GetStringValue("DisplayVersion")
				e.InstallLocation, _, _ = sub.GetStringValue("InstallLocation")
				e.UninstallString, _, _ = sub.GetStringValue("UninstallString")
				out = append(out, e)
			}
			sub.Close()
		}
	}
	return out, nil
}

// winFileVersion reads the FileVersion string of an executable's version
// resource. It does not fall back to the fixed numeric version: the installed
// CrealityPrint.exe carries 7.0.0.0 there and the build number in the string.
type winFileVersion struct{}

func (winFileVersion) FileVersion(path string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return "", err
	}
	if size == 0 {
		return "", fmt.Errorf("%s has no version resource", path)
	}
	buf := make([]byte, size)
	block := unsafe.Pointer(&buf[0])
	if err := windows.GetFileVersionInfo(path, 0, size, block); err != nil {
		return "", err
	}
	// The languages the resource is available in, then the common ones.
	var subBlocks []string
	var tr unsafe.Pointer
	var n uint32
	if windows.VerQueryValue(block, `\VarFileInfo\Translation`, unsafe.Pointer(&tr), &n) == nil && tr != nil {
		for i := uint32(0); i+4 <= n; i += 4 {
			lang := *(*uint16)(unsafe.Add(tr, i))
			page := *(*uint16)(unsafe.Add(tr, i+2))
			subBlocks = append(subBlocks, fmt.Sprintf(`\StringFileInfo\%04x%04x\FileVersion`, lang, page))
		}
	}
	for _, lp := range []string{"040904b0", "040904e4", "000004b0"} {
		subBlocks = append(subBlocks, `\StringFileInfo\`+lp+`\FileVersion`)
	}
	for _, sub := range subBlocks {
		var val *uint16
		var length uint32
		if windows.VerQueryValue(block, sub, unsafe.Pointer(&val), &length) == nil && val != nil && length > 0 {
			if s := strings.TrimSpace(windows.UTF16PtrToString(val)); s != "" {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("%s has no FileVersion string", path)
}

// winProcesses lists processes through a Toolhelp snapshot (read-only).
type winProcesses struct{}

func (winProcesses) PIDsByName(name string) ([]int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, err
	}
	var pids []int
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			pids = append(pids, int(entry.ProcessID))
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return pids, nil
}
