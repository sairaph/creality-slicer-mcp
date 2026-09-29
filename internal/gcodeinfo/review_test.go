package gcodeinfo

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReadIsOneCallForSummaryAndLayers(t *testing.T) {
	s, layers, err := Read(twoFilaments)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ReadSummary(twoFilaments)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := Layers(twoFilaments)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, s2) || !reflect.DeepEqual(layers, l2) || len(layers) != 100 {
		t.Errorf("Read must equal ReadSummary plus Layers: %d layers", len(layers))
	}
	if _, _, err := Read(twoFilaments + ".missing"); err == nil {
		t.Error("missing file")
	}
}

func TestM8200IsMatchedAsAWholeCommand(t *testing.T) {
	p := write(t, "; EXECUTABLE_BLOCK_START\nM82000 X\nM8200\n; EXECUTABLE_BLOCK_END\n")
	s, err := ReadSummary(p)
	if err != nil || !s.M8200 {
		t.Fatalf("%v %v", s.M8200, err)
	}
	p = write(t, "; EXECUTABLE_BLOCK_START\nM82000 X\nM8201\nM820\n; EXECUTABLE_BLOCK_END\n")
	s, err = ReadSummary(p)
	if err != nil || s.M8200 {
		t.Errorf("M82000 is not M8200: %v %v", s.M8200, err)
	}
	p = write(t, "; EXECUTABLE_BLOCK_START\nM8200 P1 S2\n")
	if s, _ := ReadSummary(p); !s.M8200 {
		t.Error("M8200 with parameters")
	}
}

func TestAnAbsurdlyLongLineIsRefusedNotBuffered(t *testing.T) {
	// A binary or wrong file has no newlines: the reader must stop instead of
	// growing without bound.
	huge := bytes.Repeat([]byte{'x'}, MaxLineBytes+(6<<20))
	lr := newLineReader(bytes.NewReader(huge), 0)
	_, _, _, err := lr.next()
	if !errors.Is(err, ErrLineTooLong) || !strings.Contains(err.Error(), "not a G-code file") {
		t.Fatalf("%v", err)
	}
	// Just under the limit is still fine, and lines after it are read.
	ok := append(bytes.Repeat([]byte{'y'}, 1<<20), '\n', 'z', '\n')
	lr = newLineReader(bytes.NewReader(ok), 0)
	if line, _, _, err := lr.next(); err != nil || len(line) != 1<<20 {
		t.Fatalf("%d %v", len(line), err)
	}
	if line, _, _, err := lr.next(); err != nil || string(line) != "z" {
		t.Fatalf("%q %v", line, err)
	}
	if _, _, _, err := lr.next(); err != io.EOF {
		t.Errorf("%v", err)
	}
}

func TestSummaryReportsTheLongLineError(t *testing.T) {
	p := write(t, strings.Repeat("x", MaxLineBytes+(1<<20)))
	if _, err := ReadSummary(p); !errors.Is(err, ErrLineTooLong) {
		t.Errorf("%v", err)
	}
}

func TestFixturesKeepOnlyTheConfigKeysTheTestsUse(t *testing.T) {
	for _, f := range []string{oneFilament, twoFilaments} {
		s, err := ReadSummary(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(s.Config); n > 20 {
			t.Errorf("%s keeps %d config keys: the fixtures must not carry preset templates", f, n)
		}
		for _, k := range []string{"machine_start_gcode", "filament_start_gcode", "before_layer_change_gcode", "change_filament_gcode"} {
			if _, ok := s.Config[k]; ok {
				t.Errorf("%s still has %s", f, k)
			}
		}
	}
}
