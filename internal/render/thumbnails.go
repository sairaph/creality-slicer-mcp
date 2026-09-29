package render

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Size is a thumbnail size in pixels.
type Size struct{ W, H int }

// SizeSpec is one entry of a printer preset's `thumbnails` value.
type SizeSpec struct {
	Size
	// Format is upper case: "PNG", "JPG" or "QOI". Only PNG can be embedded
	// by InsertThumbnails.
	Format string
}

// ParseThumbnailSizes parses the `thumbnails` value of a printer preset,
// "96x96/PNG, 300x300/PNG". The format is optional ("96x96,300x300" means
// PNG), entries are separated by commas, semicolons or spaces, and an empty
// value gives no sizes.
func ParseThumbnailSizes(value string) ([]SizeSpec, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
	var specs []SizeSpec
	for _, f := range fields {
		dims, format, _ := strings.Cut(f, "/")
		w, h, ok := strings.Cut(strings.ToLower(dims), "x")
		wi, err1 := strconv.Atoi(w)
		hi, err2 := strconv.Atoi(h)
		if !ok || err1 != nil || err2 != nil || wi <= 0 || hi <= 0 || wi > 8192 || hi > 8192 {
			return nil, fmt.Errorf("render: %q is not a thumbnail size such as 96x96/PNG", f)
		}
		format = strings.ToUpper(strings.TrimSpace(format))
		if format == "" {
			format = "PNG"
		}
		specs = append(specs, SizeSpec{Size: Size{wi, hi}, Format: format})
	}
	return specs, nil
}

const (
	blockStart = "; THUMBNAIL_BLOCK_START"
	blockEnd   = "; THUMBNAIL_BLOCK_END"
	thumbBegin = "; thumbnail begin"
	thumbEnd   = "; thumbnail end"
	headerEnd  = "; EXECUTABLE_BLOCK_START"
	anchor     = "; multicolor_method"
	// base64LineLen is the number of base64 characters per comment line.
	base64LineLen = 78
)

// ErrUnterminated means a thumbnail block in the header never ends before the
// header does, so the file is not rewritten.
var ErrUnterminated = errors.New("render: unterminated thumbnail block in the header")

// ErrNoAnchor means the file has no "; multicolor_method" line in its header,
// which is where the thumbnails go.
var ErrNoAnchor = errors.New(`render: no "; multicolor_method" line in the G-code header to insert the thumbnails after`)

// thumbnailBlock renders one thumbnail in the app's format:
//
//	; THUMBNAIL_BLOCK_START
//
//	;
//	; thumbnail begin 96x96 1234
//	; <78 base64 characters per line>
//	; thumbnail end
//	; THUMBNAIL_BLOCK_END
//
// where 1234 is the length of the base64 text.
func thumbnailBlock(s Size, data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	b.WriteString(blockStart + "\n\n;\n")
	fmt.Fprintf(&b, "%s %dx%d %d\n", thumbBegin, s.W, s.H, len(enc))
	for len(enc) > 0 {
		n := min(base64LineLen, len(enc))
		b.WriteString("; " + enc[:n] + "\n")
		enc = enc[n:]
	}
	b.WriteString(thumbEnd + "\n" + blockEnd + "\n\n")
	return b.String()
}

// InsertThumbnails embeds PNG thumbnails in a G-code file, one block per size
// (smallest first) right after the "; multicolor_method" line of the header,
// in the format the app writes. Thumbnail blocks already in the header are
// removed first, so calling it again with the same images gives the same file,
// and with no images it strips them. The file is streamed to a temporary file
// in the same folder and renamed over the original, so a 100 MB file is never
// held in memory and a failure leaves the original untouched.
//
// Each image must be a PNG of exactly the size it is keyed by.
func InsertThumbnails(gcodePath string, pngs map[Size][]byte) error {
	sizes := make([]Size, 0, len(pngs))
	for s, data := range pngs {
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("render: thumbnail %dx%d is not a PNG: %w", s.W, s.H, err)
		}
		if cfg.Width != s.W || cfg.Height != s.H {
			return fmt.Errorf("render: thumbnail keyed %dx%d is a %dx%d image", s.W, s.H, cfg.Width, cfg.Height)
		}
		sizes = append(sizes, s)
	}
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].W != sizes[j].W {
			return sizes[i].W < sizes[j].W
		}
		return sizes[i].H < sizes[j].H
	})
	var blocks strings.Builder
	for _, s := range sizes {
		blocks.WriteString(thumbnailBlock(s, pngs[s]))
	}

	in, err := os.Open(gcodePath)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(gcodePath), ".thumbnails-*")
	if err != nil {
		return fmt.Errorf("render: create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	out := bufio.NewWriterSize(tmp, 1<<20)
	if err := rewrite(bufio.NewReaderSize(in, 1<<20), out, blocks.String(), len(sizes) > 0); err != nil {
		return err
	}
	if err := out.Flush(); err != nil {
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	in.Close()
	if err := os.Rename(tmpName, gcodePath); err != nil {
		return fmt.Errorf("render: replace %s: %w", gcodePath, err)
	}
	done = true
	return nil
}

// rewrite copies r to w, dropping thumbnail blocks from the header and, when
// insert is set, writing blocks after the first anchor line. Past the header
// (from EXECUTABLE_BLOCK_START on) the rest is copied unread.
func rewrite(r *bufio.Reader, w io.Writer, blocks string, insert bool) error {
	inserted := !insert
	skipping := "" // "" not in a block, "marked" inside START..END, "bare" inside begin..end
	dropBlank := false
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			text := strings.TrimRight(string(line), " \t\r\n")
			switch {
			case skipping != "" && text == headerEnd:
				return ErrUnterminated
			case skipping == "marked":
				if text == blockEnd {
					skipping, dropBlank = "", true
				}
				continue
			case skipping == "bare":
				if text == thumbEnd {
					skipping = ""
				}
				continue
			case dropBlank && text == "":
				dropBlank = false
				continue
			}
			dropBlank = false
			switch {
			case text == headerEnd:
				if !inserted {
					return ErrNoAnchor
				}
				if _, werr := w.Write(line); werr != nil {
					return werr
				}
				_, cerr := r.WriteTo(w)
				return cerr
			case text == blockStart:
				skipping = "marked"
				continue
			case strings.HasPrefix(text, thumbBegin):
				skipping = "bare"
				continue
			}
			if _, werr := w.Write(line); werr != nil {
				return werr
			}
			if !inserted && strings.HasPrefix(text, anchor) {
				if !bytes.HasSuffix(line, []byte("\n")) {
					if _, werr := w.Write([]byte("\n")); werr != nil {
						return werr
					}
				}
				if _, werr := io.WriteString(w, blocks); werr != nil {
					return werr
				}
				inserted = true
			}
		}
		if err == io.EOF {
			if skipping != "" {
				return ErrUnterminated
			}
			if !inserted {
				return ErrNoAnchor
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}
