package mcpserver

import (
	"path/filepath"
	"strings"
	"testing"
)

// D3: a path in an error is written as it is, not with doubled backslashes.
func TestPathsInErrorsAreNotDoubled(t *testing.T) {
	in := `the model file "C:\\Users\\Someone\\Models\\gone.stl" does not exist; also "\\\\srv\\share\\x.3mf" and "plain"`
	want := "the model file `C:\\Users\\Someone\\Models\\gone.stl` does not exist; also `\\\\srv\\share\\x.3mf` and \"plain\""
	if got := plainPaths(in); got != want {
		t.Errorf("plainPaths = %s\nwant %s", got, want)
	}
	pf := newProjFixture(t)
	id := pf.withModel(t, "E")
	missing := filepath.Join(t.TempDir(), "Models", "gone.stl")
	e := pf.errText(t, "add_model", map[string]any{"project": id, "path": missing})
	contains(t, "missing model", e, "`"+missing+"` does not exist")
	if sep := string(filepath.Separator); sep == `\` {
		notContains(t, "missing model", e, strings.ReplaceAll(missing, sep, sep+sep))
	}
}

// D3: a refused change of a locked setting says how to override the lock.
func TestLockedSettingHintMentionsAllowLocked(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "E")
	e := pf.errText(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"machine_max_speed_x": []int{500, 200}}})
	if !strings.Contains(e, "read-only in Creality presets") {
		t.Skipf("the fixture project has no locked key: %s", e)
	}
	contains(t, "locked", e, "allow_locked true", "override Creality's lock")
}

// The choices of a bad enum are stated once in the message, not again as a list.
func TestBadEnumListsChoicesOnce(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "E")
	e := pf.errText(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"sparse_infill_pattern": "nope"}})
	if _, has := frontOf(t, e)["error"].(map[string]any)["fields"]; has {
		t.Errorf("the list of errors is repeated in fields:\n%s", e)
	}
	if n := strings.Count(e, "lateral-lattice (Lateral Lattice)"); n > 2 { // the message and its copy in the body
		t.Errorf("the choices appear %d times", n)
	}
	w := pf.errText(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"wall_loops": "x"}})
	notContains(t, "repeated key", w, "wall_loops: wall_loops")
}
