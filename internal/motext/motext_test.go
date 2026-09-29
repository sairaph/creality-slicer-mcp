package motext

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

// buildMO assembles a tiny synthetic .mo file (invented strings only).
func buildMO(pairs map[string]string, big bool) []byte {
	var order binary.ByteOrder = binary.LittleEndian
	if big {
		order = binary.BigEndian
	}
	if _, ok := pairs[""]; !ok {
		pairs[""] = "Content-Type: text/plain; charset=UTF-8\n"
	}
	ids := make([]string, 0, len(pairs))
	for id := range pairs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	n := uint32(len(ids))
	origTable := uint32(28)
	transTable := origTable + n*8
	pos := transTable + n*8
	var strs []byte
	origEnt := make([][2]uint32, n)
	transEnt := make([][2]uint32, n)
	for i, id := range ids {
		origEnt[i] = [2]uint32{uint32(len(id)), pos + uint32(len(strs))}
		strs = append(strs, id...)
		strs = append(strs, 0)
	}
	for i, id := range ids {
		tr := pairs[id]
		transEnt[i] = [2]uint32{uint32(len(tr)), pos + uint32(len(strs))}
		strs = append(strs, tr...)
		strs = append(strs, 0)
	}
	out := make([]byte, 28, int(pos)+len(strs))
	// the magic number is always written so that a reader using the file's own
	// order sees 0x950412de
	order.PutUint32(out[0:], 0x950412de)
	order.PutUint32(out[4:], 0)
	order.PutUint32(out[8:], n)
	order.PutUint32(out[12:], origTable)
	order.PutUint32(out[16:], transTable)
	tbl := func(ent [][2]uint32) []byte {
		b := make([]byte, 0, len(ent)*8)
		for _, e := range ent {
			var w [8]byte
			order.PutUint32(w[0:], e[0])
			order.PutUint32(w[4:], e[1])
			b = append(b, w[:]...)
		}
		return b
	}
	out = append(out, tbl(origEnt)...)
	out = append(out, tbl(transEnt)...)
	return append(out, strs...)
}

func TestParseBothByteOrders(t *testing.T) {
	pairs := map[string]string{"Hello": "Bonjour", "Line one\nLine two": "", "ctx\x04Open": "Ouvrir"}
	for _, big := range []bool{false, true} {
		f, err := Parse(buildMO(map[string]string{"Hello": "Bonjour", "Line one\nLine two": "", "ctx\x04Open": "Ouvrir"}, big))
		if err != nil {
			t.Fatalf("big=%v: %v", big, err)
		}
		if len(f.Entries) != len(pairs) {
			t.Fatalf("big=%v: %d entries", big, len(f.Entries))
		}
		got := map[string]Entry{}
		for _, e := range f.Entries {
			got[e.ID] = e
		}
		if got["Hello"].Str != "Bonjour" || got["Line one\nLine two"].Str != "" {
			t.Errorf("big=%v: %+v", big, got)
		}
		if e := got["Open"]; e.Context != "ctx" || e.Str != "Ouvrir" {
			t.Errorf("big=%v: context entry %+v", big, e)
		}
		if f.Header == "" {
			t.Errorf("big=%v: header lost", big)
		}
	}
}

func TestParsePluralUsesSingular(t *testing.T) {
	f, err := Parse(buildMO(map[string]string{"one file\x00many files": "un fichier\x00des fichiers"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 1 || f.Entries[0].ID != "one file" || f.Entries[0].Str != "un fichier" {
		t.Errorf("plural entry = %+v", f.Entries)
	}
}

func TestParseErrors(t *testing.T) {
	good := buildMO(map[string]string{"a": "b"}, false)
	cases := map[string][]byte{
		"short":   {1, 2, 3},
		"magic":   append([]byte{0, 0, 0, 0}, good[4:]...),
		"table":   good[:30],
		"charset": buildMO(map[string]string{"": "Content-Type: text/plain; charset=Shift_JIS\n", "a": "b"}, false),
	}
	for name, data := range cases {
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// a string offset past the end of the file
	bad := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(bad[28+8+4:], 1<<30)
	if _, err := Parse(bad); err == nil {
		t.Error("out of range string offset accepted")
	}
	// unsupported major revision
	rev := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(rev[4:], 5<<16)
	if _, err := Parse(rev); err == nil {
		t.Error("unsupported revision accepted")
	}
}

func TestHashMatchesFNV1a(t *testing.T) {
	if Hash("") != 0xcbf29ce484222325 || Hash("a") != 0xaf63dc4c8601ec8c {
		t.Errorf("Hash is not FNV-1a 64: %x %x", Hash(""), Hash("a"))
	}
	if HashHex("a") != "af63dc4c8601ec8c" || len(HashHex("")) != 16 {
		t.Errorf("HashHex = %s", HashHex("a"))
	}
}

func write(t *testing.T, dir, lang string, data []byte) {
	t.Helper()
	d := filepath.Join(dir, lang)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirUnionAndEnglishOverride(t *testing.T) {
	dir := t.TempDir()
	// "en" fixes the wording of one message id and leaves another untranslated
	write(t, dir, "en", buildMO(map[string]string{"Speed of outer wal": "Speed of the outer wall", "Kept as is": ""}, false))
	// other languages contribute message ids the English file does not have
	write(t, dir, "de", buildMO(map[string]string{"Only in German file": "Nur hier", "Kept as is": "Bleibt"}, true))
	write(t, dir, "fr", buildMO(map[string]string{"Only in French file": "Seulement ici"}, false))
	// a language directory without a catalog, and one with a broken catalog
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "xx", []byte("not a mo file, but long enough to pass the length check........"))

	ix, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Langs(); len(got) != 3 || got[0] != "de" || got[1] != "en" || got[2] != "fr" {
		t.Errorf("langs = %v", got)
	}
	if _, bad := ix.Skipped()["xx"]; !bad || len(ix.Skipped()) != 1 {
		t.Errorf("skipped = %v", ix.Skipped())
	}
	cases := map[string]string{
		"Speed of outer wal":  "Speed of the outer wall", // en msgstr replaces the msgid text
		"Kept as is":          "Kept as is",              // empty en msgstr keeps the msgid text
		"Only in German file": "Only in German file",     // union over languages, msgid text
		"Only in French file": "Only in French file",
	}
	for id, want := range cases {
		got, ok := ix.Text(Hash(id))
		if !ok || got != want {
			t.Errorf("Text(%q) = %q, %v; want %q", id, got, ok, want)
		}
	}
	if ix.Len() != 4 {
		t.Errorf("Len = %d, want 4", ix.Len())
	}
	if _, ok := ix.Text(Hash("Nur hier")); ok {
		t.Error("translated text of another language leaked into the index")
	}
}

func TestLoadDirErrors(t *testing.T) {
	if _, err := LoadDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing directory accepted")
	}
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Error("directory without catalogs accepted")
	}
}

func TestNilIndex(t *testing.T) {
	var ix *Index
	if _, ok := ix.Text(1); ok || ix.Len() != 0 || ix.Langs() != nil || len(ix.Skipped()) != 0 {
		t.Error("nil index must behave as empty")
	}
	if got, ok := NewIndex([]string{"x"}).Text(Hash("x")); !ok || got != "x" {
		t.Error("NewIndex")
	}
}
