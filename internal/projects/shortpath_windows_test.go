package projects

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func shortPathOf(p string) (string, error) {
	in, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	return syscall.UTF16ToString(buf[:n]), nil
}

// A file exported through the long folder name and opened through its Windows
// short (8.3) name is the same file: the spool links come back.
func TestExportedPathMatchesThroughAShortName(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Short")
	long := filepath.Join(t.TempDir(), "A Rather Long Folder Name With Spaces")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(long, "exported project.3mf")
	if _, err := e.st.Export(info.ID, file, false); err != nil {
		t.Fatal(err)
	}
	// The app drops the member when it saves: only the export record can find the links.
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	stripped, err := withSpoolMember(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, stripped, 0o644); err != nil {
		t.Fatal(err)
	}
	shortDir, err := shortPathOf(long)
	if err != nil || strings.EqualFold(shortDir, long) {
		t.Skip("8.3 names are not generated on this volume")
	}
	res, err := e.st.OpenProject(OpenRequest{Path: filepath.Join(shortDir, "EXPORT~1.3MF")})
	if err != nil {
		// the short file name may differ: ask for the short form of the file itself
		short, serr := shortPathOf(file)
		if serr != nil {
			t.Skipf("no short form of the file: %v", serr)
		}
		if res, err = e.st.OpenProject(OpenRequest{Path: short}); err != nil {
			t.Fatal(err)
		}
	}
	if res.SpoolsFrom == "" || strings.Join(slotsOf(res.Info), ",") != "T2C,T1A,T3B" {
		t.Fatalf("slots %v from %q", slotsOf(res.Info), res.SpoolsFrom)
	}
}
