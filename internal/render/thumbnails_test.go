package render

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
)

const fixture = "../gcodeinfo/testdata/cube_1filament.gcode"

func TestParseThumbnailSizes(t *testing.T) {
	cases := []struct {
		in   string
		want []SizeSpec
		bad  bool
	}{
		{in: "96x96/PNG, 300x300/PNG", want: []SizeSpec{{Size{96, 96}, "PNG"}, {Size{300, 300}, "PNG"}}},
		{in: "96x96,300x300", want: []SizeSpec{{Size{96, 96}, "PNG"}, {Size{300, 300}, "PNG"}}},
		{in: " 48X48/jpg ; 64x32 ", want: []SizeSpec{{Size{48, 48}, "JPG"}, {Size{64, 32}, "PNG"}}},
		{in: "", want: nil},
		{in: "  ", want: nil},
		{in: "96", bad: true},
		{in: "96x", bad: true},
		{in: "axb/PNG", bad: true},
		{in: "0x10", bad: true},
		{in: "-5x10", bad: true},
		{in: "96x96/PNG, nonsense", bad: true},
		{in: "99999x10", bad: true},
	}
	for _, c := range cases {
		got, err := ParseThumbnailSizes(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q: no error, got %v", c.in, got)
			}
			continue
		}
		if err != nil || fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%q: got %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

// testPNG is a w by h PNG of one colour.
func testPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	data, err := encodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func copyFixture(t *testing.T) (path string, original []byte) {
	t.Helper()
	original, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "plate_1.gcode")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, original
}

func plateThumbs(t *testing.T) map[Size][]byte {
	t.Helper()
	imgs, err := PlateImages(threeBoxes())
	if err != nil {
		t.Fatal(err)
	}
	return map[Size][]byte{{96, 96}: imgs["plate_2_small.png"], {300, 300}: imgs["plate_2.png"]}
}

// wantBlock builds the expected text of one block from the format's rules,
// independently of the code under test.
func wantBlock(w, h int, data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	lines := []string{"; THUMBNAIL_BLOCK_START", "", ";", fmt.Sprintf("; thumbnail begin %dx%d %d", w, h, len(enc))}
	for i := 0; i < len(enc); i += 78 {
		lines = append(lines, "; "+enc[i:min(i+78, len(enc))])
	}
	lines = append(lines, "; thumbnail end", "; THUMBNAIL_BLOCK_END", "")
	return strings.Join(lines, "\n") + "\n"
}

func TestInsertThumbnailsFormat(t *testing.T) {
	small, big := testPNG(t, 8, 8, red), testPNG(t, 40, 40, blue)
	path := filepath.Join(t.TempDir(), "a.gcode")
	src := "; HEADER_BLOCK_START\n; total layer number: 3\n; HEADER_BLOCK_END\n\n; multicolor_method = 0 \n; other = 1\n; EXECUTABLE_BLOCK_START\nG1 X1\n; EXECUTABLE_BLOCK_END\n; multicolor_method = 0\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// Given largest first: the file lists the smaller size first.
	if err := InsertThumbnails(path, map[Size][]byte{{40, 40}: big, {8, 8}: small}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "; HEADER_BLOCK_START\n; total layer number: 3\n; HEADER_BLOCK_END\n\n; multicolor_method = 0 \n" +
		wantBlock(8, 8, small) + wantBlock(40, 40, big) +
		"; other = 1\n; EXECUTABLE_BLOCK_START\nG1 X1\n; EXECUTABLE_BLOCK_END\n; multicolor_method = 0\n"
	if string(got) != want {
		t.Errorf("file after insert:\n%s\nwant:\n%s", got, want)
	}
	// Every base64 line is 78 characters or fewer, the last shorter.
	for _, l := range strings.Split(string(got), "\n") {
		if strings.HasPrefix(l, "; ") && !strings.Contains(l[2:], " ") && len(l) > 80 {
			t.Errorf("line of %d characters: %.40s", len(l), l)
		}
	}
}

func TestInsertThumbnailsIntoARealFileAndReadBack(t *testing.T) {
	path, original := copyFixture(t)
	thumbs := plateThumbs(t)
	if err := InsertThumbnails(path, thumbs); err != nil {
		t.Fatal(err)
	}
	s, err := gcodeinfo.ReadSummary(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Thumbnails) != 2 {
		t.Fatalf("gcodeinfo reads %d thumbnails: %+v", len(s.Thumbnails), s.Thumbnails)
	}
	small, big := s.Thumbnails[0], s.Thumbnails[1]
	if small.Width != 96 || small.Height != 96 || big.Width != 300 || big.Height != 300 {
		t.Errorf("thumbnails read back as %+v", s.Thumbnails)
	}
	if want := len(base64.StdEncoding.EncodeToString(thumbs[Size{96, 96}])); small.Length != want {
		t.Errorf("declared length %d, want %d", small.Length, want)
	}
	// The multicolor_method line of the header is line 17 of the fixture, so the
	// first "thumbnail begin" is the fourth line of the block after it.
	if small.Line != 21 {
		t.Errorf("first thumbnail begins on line %d, want 21", small.Line)
	}
	// The payload decodes to a PNG of the size the block declares.
	for _, want := range []Size{{96, 96}, {300, 300}} {
		data := extractPayload(t, path, want)
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != want.W || cfg.Height != want.H {
			t.Errorf("%v: payload decodes to %+v, %v", want, cfg, err)
		}
		if !bytes.Equal(data, thumbs[want]) {
			t.Errorf("%v: the payload is not the image that was inserted", want)
		}
	}
	// Nothing else changed: taking the blocks out gives the original bytes.
	if err := InsertThumbnails(path, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, original) {
		t.Error("stripping the thumbnails did not restore the original file")
	}
}

// extractPayload decodes the base64 of the block of the given size.
func extractPayload(t *testing.T, path string, s Size) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var enc strings.Builder
	in := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, fmt.Sprintf("; thumbnail begin %dx%d ", s.W, s.H)):
			in = true
		case in && line == "; thumbnail end":
			data, err := base64.StdEncoding.DecodeString(enc.String())
			if err != nil {
				t.Fatal(err)
			}
			return data
		case in:
			enc.WriteString(strings.TrimPrefix(line, "; "))
		}
		if !in && len(line) > 0 && line[0] != ';' {
			break // past the header
		}
	}
	t.Fatalf("no %v thumbnail in the file", s)
	return nil
}

func TestInsertThumbnailsIsIdempotentAndReplaces(t *testing.T) {
	path, _ := copyFixture(t)
	thumbs := plateThumbs(t)
	if err := InsertThumbnails(path, thumbs); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := InsertThumbnails(path, thumbs); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("inserting the same thumbnails twice changed the file")
	}
	if n := strings.Count(string(second), "; THUMBNAIL_BLOCK_START"); n != 2 {
		t.Errorf("%d blocks after two inserts, want 2", n)
	}

	// New images replace the old ones; a size that is no longer given goes.
	other := map[Size][]byte{{96, 96}: testPNG(t, 96, 96, green)}
	if err := InsertThumbnails(path, other); err != nil {
		t.Fatal(err)
	}
	s, _ := gcodeinfo.ReadSummary(path)
	if len(s.Thumbnails) != 1 || s.Thumbnails[0].Width != 96 {
		t.Errorf("thumbnails after replacing: %+v", s.Thumbnails)
	}
	if !bytes.Equal(extractPayload(t, path, Size{96, 96}), other[Size{96, 96}]) {
		t.Error("the replacement image is not in the file")
	}
	// The second multicolor_method line (in the config block) got nothing.
	if n := strings.Count(mustRead(t, path), "; THUMBNAIL_BLOCK_START"); n != 1 {
		t.Errorf("%d blocks, want 1", n)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInsertThumbnailsRemovesBlocksTheAppWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.gcode")
	src := "; HEADER_BLOCK_START\n; HEADER_BLOCK_END\n\n" +
		"; THUMBNAIL_BLOCK_START\n\n;\n; thumbnail begin 16x16 8\n; AAAAAAAA\n; thumbnail end\n; THUMBNAIL_BLOCK_END\n\n" +
		"; multicolor_method = 0\n" +
		"; thumbnail begin 32x32 4\n; BBBB\n; thumbnail end\n" +
		"; EXECUTABLE_BLOCK_START\nG1 X1\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	png8 := testPNG(t, 8, 8, red)
	if err := InsertThumbnails(path, map[Size][]byte{{8, 8}: png8}); err != nil {
		t.Fatal(err)
	}
	want := "; HEADER_BLOCK_START\n; HEADER_BLOCK_END\n\n; multicolor_method = 0\n" + wantBlock(8, 8, png8) + "; EXECUTABLE_BLOCK_START\nG1 X1\n"
	if got := mustRead(t, path); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestInsertThumbnailsWithoutAnAnchorLeavesTheFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.gcode")
	src := "; HEADER_BLOCK_START\n; HEADER_BLOCK_END\n; EXECUTABLE_BLOCK_START\nG1 X1\n; multicolor_method = 0\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InsertThumbnails(path, map[Size][]byte{{8, 8}: testPNG(t, 8, 8, red)})
	if !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("err = %v, want ErrNoAnchor", err)
	}
	if got := mustRead(t, path); got != src {
		t.Errorf("the file changed:\n%s", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	// With nothing to insert, no anchor is needed.
	if err := InsertThumbnails(path, nil); err != nil {
		t.Errorf("stripping a file without an anchor: %v", err)
	}
}

func TestInsertThumbnailsRefusesBadImages(t *testing.T) {
	path, original := copyFixture(t)
	dir := filepath.Dir(path)
	cases := map[string]map[Size][]byte{
		"not a png":     {{8, 8}: []byte("nope")},
		"wrong size":    {{16, 16}: testPNG(t, 8, 8, red)},
		"one good, bad": {{8, 8}: testPNG(t, 8, 8, red), {9, 9}: testPNG(t, 8, 8, red)},
	}
	for name, thumbs := range cases {
		if err := InsertThumbnails(path, thumbs); err == nil {
			t.Errorf("%s: no error", name)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
			t.Errorf("%s: the file changed", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	if err := InsertThumbnails(filepath.Join(dir, "missing.gcode"), nil); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestInsertThumbnailsAddsANewlineAfterAnUnterminatedAnchor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.gcode")
	if err := os.WriteFile(path, []byte("; multicolor_method = 0"), 0o644); err != nil {
		t.Fatal(err)
	}
	png8 := testPNG(t, 8, 8, red)
	if err := InsertThumbnails(path, map[Size][]byte{{8, 8}: png8}); err != nil {
		t.Fatal(err)
	}
	if got, want := mustRead(t, path), "; multicolor_method = 0\n"+wantBlock(8, 8, png8); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestInsertThumbnailsKeepsThePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to keep")
	}
	path, _ := copyFixture(t)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := InsertThumbnails(path, plateThumbs(t)); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}

// A file of 100 MB is streamed: the rewrite must be fast and leave the body
// byte for byte as it was.
func TestInsertThumbnailsStreamsALargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.gcode")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	fmt.Fprint(w, "; HEADER_BLOCK_START\n; HEADER_BLOCK_END\n\n; multicolor_method = 0 \n; EXECUTABLE_BLOCK_START\n")
	line := "G1 X123.456 Y234.567 E0.0123 F3000 ; a body line of ordinary length for the test\n"
	body := 0
	for body < 100<<20 {
		w.WriteString(line)
		body += len(line)
	}
	fmt.Fprint(w, "; EXECUTABLE_BLOCK_END\n; multicolor_method = 0\n")
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	before, _ := os.Stat(path)

	thumbs := map[Size][]byte{{96, 96}: testPNG(t, 96, 96, red)}
	start := time.Now()
	if err := InsertThumbnails(path, thumbs); err != nil {
		t.Fatal(err)
	}
	t.Logf("inserted into %d MB in %v", before.Size()>>20, time.Since(start))
	after, _ := os.Stat(path)
	added := int64(len(thumbnailBlock(Size{96, 96}, thumbs[Size{96, 96}])))
	if after.Size() != before.Size()+added {
		t.Errorf("size grew by %d, want %d", after.Size()-before.Size(), added)
	}
	s, err := gcodeinfo.ReadSummary(path)
	if err != nil || len(s.Thumbnails) != 1 {
		t.Fatalf("read back: %+v, %v", s.Thumbnails, err)
	}
	if err := InsertThumbnails(path, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.Stat(path); again.Size() != before.Size() {
		t.Errorf("after stripping the size is %d, want %d", again.Size(), before.Size())
	}
}

// RN1: a thumbnail block that never ends must not swallow the file body; the
// file is left as it was.
func TestInsertThumbnailsRefusesAnUnterminatedBlock(t *testing.T) {
	for name, src := range map[string]string{
		"header ends first": "; HEADER_BLOCK_START\n; multicolor_method = 0\n; THUMBNAIL_BLOCK_START\n; thumbnail begin 8x8 10\n; AAAA\n; HEADER_BLOCK_END\n; EXECUTABLE_BLOCK_START\nG1 X1\n",
		"file ends first":   "; HEADER_BLOCK_START\n; multicolor_method = 0\n; THUMBNAIL_BLOCK_START\n; thumbnail begin 8x8 10\n; AAAA\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.gcode")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			err := InsertThumbnails(path, map[Size][]byte{{8, 8}: testPNG(t, 8, 8, red)})
			if !errors.Is(err, ErrUnterminated) {
				t.Fatalf("err = %v, want ErrUnterminated", err)
			}
			if got := mustRead(t, path); got != src {
				t.Errorf("the file changed:\n%s", got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("temporary files left behind: %v", entries)
			}
		})
	}
}

// RN2: an image size above MaxSize is refused instead of allocating it.
func TestSizesAboveMaxSizeAreRefused(t *testing.T) {
	if _, err := Preview(threeBoxes(), ViewIso, MaxSize+1); err == nil {
		t.Error("Preview accepted a size above MaxSize")
	}
	if _, err := GCodeLayer(nil, ByFeature, MaxSize+1, LayerOptions{}); err == nil {
		t.Error("GCodeLayer accepted a size above MaxSize")
	}
}
