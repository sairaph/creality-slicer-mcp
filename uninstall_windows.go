//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// removeFromUserPath removes dir from the user PATH in the registry, where
// install.ps1 added it, keeping the value's type (REG_SZ or REG_EXPAND_SZ)
// and every other entry as written, then tells running programs.
func removeFromUserPath(dir string) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	// GetStringValue returns REG_EXPAND_SZ data unexpanded.
	value, typ, err := k.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	updated, removed := removePathListEntry(value, dir, ";", true)
	if !removed {
		return false, nil
	}
	if typ == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", updated)
	} else {
		err = k.SetStringValue("Path", updated)
	}
	if err != nil {
		return false, err
	}
	broadcastEnvironmentChange()
	return true, nil
}

// broadcastEnvironmentChange sends WM_SETTINGCHANGE("Environment"), as
// .NET's SetEnvironmentVariable does, so Explorer and terminals started from
// it pick up the new PATH.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}

// removeInstallRoot schedules removal of the folder that holds the install
// directory (%LOCALAPPDATA%\creality-slicer-mcp, which holds nothing else) by a
// detached helper, because Windows keeps a running executable locked until its
// process exits. The helper waits for this process to end, then deletes the
// directory.
func removeInstallRoot(installDir string) error {
	root := filepath.Dir(installDir)
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	quoted := "'" + strings.ReplaceAll(root, "'", "''") + "'"
	script := fmt.Sprintf("Wait-Process -Id %d -ErrorAction SilentlyContinue; Remove-Item -LiteralPath %s -Recurse -Force",
		os.Getpid(), quoted)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	// Run from outside the directory being deleted.
	cmd.Dir = os.TempDir()
	// CREATE_NO_WINDOW gives the helper its own hidden console, so it keeps
	// running when this terminal closes. DETACHED_PROCESS does not work
	// here: powershell.exe started without any console exits before
	// running its command (observed on Windows 11 with PowerShell 5.1).
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	return cmd.Start()
}
