package slicer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

// helperEnv switches the test binary into a fake CrealityPrint: it is
// re-executed by the exec-path tests with this variable set and behaves as
// the variable says. Nothing here ever starts the real slicer.
const helperEnv = "CREALITY_SLICER_MCP_TEST_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		helperMain(mode)
		return
	}
	os.Exit(testhome.Run(m))
}

// helperMain acts as a fake slicer.
//
// Modes:
//
//	slice  write plate_N.gcode into --outputdir, print launcher noise and a log
//	       line to stdout, HELPER_STDERR to stderr, exit HELPER_EXIT (default 0)
//	sleep  print "started" and sleep for an hour
//	tree   start a "sleep" copy of itself, write its pid to HELPER_PIDFILE, sleep
//	dump   write HELPER_DUMP (a fake crash dump) and exit like an access violation
func helperMain(mode string) {
	args := os.Args[1:]
	value := func(flag string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag {
				return args[i+1]
			}
		}
		return ""
	}
	switch mode {
	case "slice", "dump":
		outDir := value("--outputdir")
		plate := 1
		if n, err := strconv.Atoi(value("--slice")); err == nil && n > 0 {
			plate = n
		}
		fmt.Println("OpenGL probe: version 4.6")
		fmt.Println("[2026-09-29 07:03:52.123456] [0x00001a2b] [info] loading the model")
		fmt.Println("[2026-09-29 07:03:52.223456] [0x00001a2b] [error] something real went wrong")
		if msg := os.Getenv("HELPER_STDERR"); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
		if os.Getenv("HELPER_NOFILE") == "" && mode == "slice" {
			_ = os.WriteFile(filepath.Join(outDir, fmt.Sprintf("plate_%d.gcode", plate)), []byte("; fake\n"), 0o644)
		}
		if mode == "dump" {
			_ = os.WriteFile(os.Getenv("HELPER_DUMP"), []byte("MDMP"), 0o644)
			os.Exit(int(int32(-1073741819)))
		}
		code, _ := strconv.Atoi(os.Getenv("HELPER_EXIT"))
		os.Exit(code)
	case "sleep":
		fmt.Println("started")
		time.Sleep(2 * time.Minute) // a failed kill must not leak a process for long
		os.Exit(92)
	case "tree":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			os.Exit(90)
		}
		_ = os.WriteFile(os.Getenv("HELPER_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		fmt.Println("started")
		time.Sleep(2 * time.Minute) // a failed kill must not leak a process for long
		os.Exit(92)
	}
	os.Exit(91)
}
