// Package motext reads GNU gettext .mo catalogs and builds a lookup of the
// installed application's own UI texts (tooltips) keyed by a hash of the
// message id. The catalog package stores only that hash for each setting, so
// none of Creality's tooltip wording is committed; it is looked up at run time
// from the copy of the application installed on the user's machine.
package motext

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	magicLE = 0x950412de // magic as read when the file is little endian
	magicBE = 0xde120495 // the same magic read little endian from a big endian file

	// SourceLang is the language directory whose translations override the
	// message ids themselves (the message id is the English source text, and
	// an English catalog can carry corrected wording).
	SourceLang = "en"
	// FileName is the catalog file name inside each language directory.
	FileName = "CrealityPrint.mo"
)

// Hash returns the FNV-1a 64-bit hash of a message id, the key used by the
// slim catalog's tooltip_hash.
func Hash(msgid string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(msgid))
	return h.Sum64()
}

// HashHex is Hash as 16 lowercase hex digits.
func HashHex(msgid string) string { return fmt.Sprintf("%016x", Hash(msgid)) }

// Entry is one translation unit of a .mo file.
type Entry struct {
	Context string // msgctxt, empty when the entry has none
	ID      string // msgid (singular form for plural entries)
	Str     string // msgstr (first form for plural entries); may be empty when untranslated
}

// File is a parsed .mo file.
type File struct {
	Header  string // the metadata text (the translation of the empty msgid)
	Entries []Entry
}

// Parse reads a .mo file in either byte order.
func Parse(data []byte) (*File, error) {
	if len(data) < 28 {
		return nil, errors.New("motext: file too short for a .mo header")
	}
	var order binary.ByteOrder
	switch binary.LittleEndian.Uint32(data[0:4]) {
	case magicLE:
		order = binary.LittleEndian
	case magicBE:
		order = binary.BigEndian
	default:
		return nil, errors.New("motext: bad magic number, not a .mo file")
	}
	revision := order.Uint32(data[4:8])
	if major := revision >> 16; major > 1 {
		return nil, fmt.Errorf("motext: unsupported .mo revision %d.%d", major, revision&0xffff)
	}
	n := order.Uint32(data[8:12])
	origOff := order.Uint32(data[12:16])
	transOff := order.Uint32(data[16:20])
	size := uint64(len(data))
	if uint64(origOff)+uint64(n)*8 > size || uint64(transOff)+uint64(n)*8 > size {
		return nil, errors.New("motext: string tables exceed the file size")
	}
	str := func(table uint32, i uint32) (string, error) {
		p := table + i*8
		length, off := order.Uint32(data[p:p+4]), order.Uint32(data[p+4:p+8])
		if uint64(off)+uint64(length) > size {
			return "", fmt.Errorf("motext: string %d exceeds the file size", i)
		}
		return string(data[off : off+length]), nil
	}
	f := &File{}
	for i := uint32(0); i < n; i++ {
		id, err := str(origOff, i)
		if err != nil {
			return nil, err
		}
		tr, err := str(transOff, i)
		if err != nil {
			return nil, err
		}
		if id == "" {
			f.Header = tr
			continue
		}
		e := Entry{}
		if j := strings.IndexByte(id, 0x04); j >= 0 { // msgctxt EOT msgid
			e.Context, id = id[:j], id[j+1:]
		}
		if j := strings.IndexByte(id, 0); j >= 0 { // msgid NUL msgid_plural: keep the singular
			id = id[:j]
		}
		if j := strings.IndexByte(tr, 0); j >= 0 { // plural forms: keep the first
			tr = tr[:j]
		}
		e.ID, e.Str = id, tr
		f.Entries = append(f.Entries, e)
	}
	if err := checkCharset(f.Header); err != nil {
		return nil, err
	}
	return f, nil
}

// checkCharset accepts UTF-8 (and US-ASCII) catalogs, which is what the
// application ships; anything else would need a conversion this reader does
// not do, so it is reported instead of returning garbled text.
func checkCharset(header string) error {
	for _, line := range strings.Split(header, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		if !strings.HasPrefix(l, "content-type:") {
			continue
		}
		if i := strings.Index(l, "charset="); i >= 0 {
			cs := strings.TrimSpace(l[i+len("charset="):])
			if cs == "" || cs == "utf-8" || cs == "utf8" || cs == "ascii" || cs == "us-ascii" || cs == "charset" {
				return nil
			}
			return fmt.Errorf("motext: unsupported catalog charset %q", cs)
		}
	}
	return nil
}

// ReadFile parses the .mo file at path.
func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Index maps the hash of a message id to display text: the union of the
// message ids of every language found, with the English translation replacing
// the message id text where it is non-empty.
//
// Known limit: a message context (msgctxt) is parsed but not part of the hash,
// so a contexted entry and a context-free entry with the same message id share
// one hash and the first one seen wins. The settings' tooltips are context-free
// message ids, so this does not affect them.
type Index struct {
	m       map[uint64]string
	langs   []string
	skipped map[string]string
}

// LoadDir loads every <dir>/<lang>/CrealityPrint.mo. Unreadable or malformed
// catalogs are skipped and reported by Skipped; it is an error when no catalog
// could be loaded at all.
func LoadDir(dir string) (*Index, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	ix := &Index{m: map[uint64]string{}, skipped: map[string]string{}}
	files := map[string]*File{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), FileName)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		f, err := ReadFile(p)
		if err != nil {
			ix.skipped[e.Name()] = err.Error()
			continue
		}
		files[e.Name()] = f
		ix.langs = append(ix.langs, e.Name())
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("motext: no %s found under %s", FileName, dir)
	}
	sort.Strings(ix.langs)
	// union of message ids first, so the result does not depend on a language's rank
	for _, lang := range ix.langs {
		for _, e := range files[lang].Entries {
			h := Hash(e.ID)
			if _, ok := ix.m[h]; !ok {
				ix.m[h] = e.ID
			}
		}
	}
	if en := files[SourceLang]; en != nil {
		for _, e := range en.Entries {
			if e.Str != "" {
				ix.m[Hash(e.ID)] = e.Str
			}
		}
	}
	return ix, nil
}

// NewIndex builds an Index directly from message texts (used by tests and by
// callers that already have the strings).
func NewIndex(texts []string) *Index {
	ix := &Index{m: make(map[uint64]string, len(texts)), skipped: map[string]string{}}
	for _, t := range texts {
		ix.m[Hash(t)] = t
	}
	return ix
}

// Text returns the text whose message id hashes to h.
func (ix *Index) Text(h uint64) (string, bool) {
	if ix == nil {
		return "", false
	}
	s, ok := ix.m[h]
	return s, ok
}

// Len is the number of distinct message ids.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.m)
}

// Langs lists the languages that were loaded, sorted.
func (ix *Index) Langs() []string {
	if ix == nil {
		return nil
	}
	return append([]string(nil), ix.langs...)
}

// Skipped maps a language to why its catalog could not be used.
func (ix *Index) Skipped() map[string]string {
	out := map[string]string{}
	if ix != nil {
		for k, v := range ix.skipped {
			out[k] = v
		}
	}
	return out
}
