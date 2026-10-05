package projects

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// spoolMember is the 3MF member an export carries the CFS spool links in, so
// open_project of that file can restore them. The app drops members it does not
// know when it saves, which is why meta.Exports is the second source.
const spoolMember = "Metadata/creality_slicer_mcp.json"

type spoolMemberDoc struct {
	Spools []SpoolLink `json:"spools"`
}

// withSpoolMember returns a copy of a 3MF with the spool member replaced: any
// existing member of that name is dropped (it would be stale), and one with the
// links is added when there are some. The other entries are copied as they are,
// in order.
func withSpoolMember(data []byte, links []SpoolLink) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if f.Name == spoolMember {
			continue
		}
		if err := zw.Copy(f); err != nil {
			return nil, err
		}
	}
	if len(links) > 0 {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: spoolMember, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return nil, err
		}
		doc, err := json.MarshalIndent(spoolMemberDoc{Spools: links}, "", " ")
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(doc); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// embeddedSpools reads the spool links an export left in a 3MF (nil when the
// file has none or they cannot be read).
func embeddedSpools(path string) []SpoolLink {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != spoolMember {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		if err != nil {
			return nil
		}
		var doc spoolMemberDoc
		if json.Unmarshal(data, &doc) != nil {
			return nil
		}
		return doc.Spools
	}
	return nil
}

// leadingLinks keeps the unbroken run of links, from the first filament on,
// whose preset is still the preset of that filament.
func leadingLinks(links []SpoolLink, presets []string) []SpoolLink {
	var out []SpoolLink
	for i, l := range links {
		if i >= len(presets) || presets[i] != l.Preset {
			break
		}
		out = append(out, l)
	}
	return out
}

// samePath compares two file paths the way the platform does (Windows paths
// ignore case). Two spellings of one place also match: the folders are compared
// with their links resolved (junctions, symbolic links, 8.3 short names) when
// they exist.
func samePath(a, b string) bool {
	eq := func(x, y string) bool {
		if runtime.GOOS == "windows" {
			return strings.EqualFold(x, y)
		}
		return x == y
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	return eq(a, b) || eq(resolvedFile(a), resolvedFile(b))
}

// resolvedFile is a file path with the links of its folder resolved; a folder
// that does not exist leaves the path as it is.
func resolvedFile(p string) string {
	dir, base := filepath.Split(p)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return filepath.Join(dir, base)
}

// addExport records an exported path in the project's metadata (no new
// revision): cleaned, once.
func (m *meta) addExport(path string) {
	path = filepath.Clean(path)
	for _, e := range m.Exports {
		if samePath(e, path) {
			return
		}
	}
	m.Exports = append(m.Exports, path)
}

// exportedBy finds the project that exported the file at path: the most
// recently changed one when several did.
func (s *Store) exportedBy(path string) (id string, links []SpoolLink) {
	entries, err := readDirNames(s.cfg.Root)
	if err != nil {
		return "", nil
	}
	var best *meta
	for _, name := range entries {
		m, err := s.readMeta(name)
		if err != nil {
			continue
		}
		for _, e := range m.Exports {
			if samePath(e, path) {
				if best == nil || m.Updated.After(best.Updated) {
					best, id = m, name
				}
				break
			}
		}
	}
	if best == nil {
		return "", nil
	}
	return id, best.Spools
}

func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), trashPrefix) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
