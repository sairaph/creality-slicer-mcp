package catalog

import (
	"os"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

// fixtureJSON is a small invented catalog in the slim layout (no Creality text).
const fixtureJSON = `{
 "format": 1,
 "source": {"ref": "vTest", "commit": "abc"},
 "options": [
  {"key":"layer_height","owner":"process","preset_types":["process"],"value_type":"float","is_vector":false,"nullable":false,"default":0.2,"min":0,"max":100,
   "label":"Layer height","sidetext":"mm","category":"Quality","ui_level":"simple","gui":{"tab":"process","page":"Quality","group":"Layer height"},
   "scopes":["object","layer_range"],"cli_flags":["--layer-height"],"tooltip_hash":"0000000000000001"},
  {"key":"sparse_infill_density","owner":"process","preset_types":["process"],"value_type":"percent","is_vector":false,"nullable":false,"default":20,"min":0,"max":100,
   "label":"Sparse infill density","sidetext":"%","category":"Strength","ui_level":"simple","gui":{"tab":"process","page":"Strength","group":"Infill"},
   "scopes":["object","part","layer_range"],"cli_flags":["--sparse-infill-density"],"tooltip_hash":"0000000000000002"},
  {"key":"line_width","owner":"process","preset_types":["process"],"value_type":"float_or_percent","is_vector":false,"nullable":false,"default":{"value":0,"percent":false},"min":0,"max":1000,
   "label":"Default","sidetext":"mm or %","category":"Quality","ui_level":"advanced","gui":{"tab":"process","page":"Quality","group":"Line width"},
   "scopes":["object"],"cli_flags":["--line-width"]},
  {"key":"enable_support","owner":"process","preset_types":["process"],"value_type":"bool","is_vector":false,"nullable":false,"default":false,
   "label":"Enable support","category":"Support","ui_level":"simple","gui":{"tab":"process","page":"Support","group":"Support"},
   "scopes":["object","part"],"cli_flags":["--enable-support"],"tooltip_hash":"0000000000000003",
   "forced_by":[{"set":"off","when":[{"k":"and","a":[{"k":"opt","key":"spiral_mode","kind":"bool"},{"k":"not","a":[{"k":"cap","name":"is_plate_config"}]}]}]}]},
  {"key":"support_type","owner":"process","preset_types":["process"],"value_type":"enum","is_vector":false,"nullable":false,"default":"normal_auto",
   "enum":{"values":["normal_auto","tree_auto"],"labels":["Normal (auto)","Tree (auto)"]},"label":"Type","category":"Support","ui_level":"simple",
   "gui":{"tab":"process","page":"Support","group":"Support"},"scopes":["object"],"cli_flags":["--support-type"],
   "gated_by":[{"effect":"field","when":{"k":"or","a":[{"k":"opt","key":"enable_support","kind":"bool"},{"k":"cmp","op":">","a":[{"k":"opt","key":"raft_layers","kind":"int"},{"k":"num","v":0}]}]},"drivers":["enable_support","raft_layers"]},
             {"effect":"line","when":{"k":"not","a":[{"k":"cap","name":"is_belt_machine"}]}}]},
  {"key":"support_threshold_angle","owner":"process","preset_types":["process"],"value_type":"int","is_vector":false,"nullable":false,"default":30,"min":1,"max":90,
   "label":"Threshold angle","sidetext":"deg","category":"Support","ui_level":"advanced","gui":{"tab":"process","page":"Support","group":"Support"},"scopes":["object"],
   "gated_by":[{"effect":"field","when":{"k":"and","a":[{"k":"opt","key":"enable_support","kind":"bool"},{"k":"call","name":"is_auto","a":[{"k":"opt","key":"support_type","kind":"enum","t":"SupportType"}]}]},"drivers":["enable_support","support_type"]}]},
  {"key":"raft_layers","owner":"process","preset_types":["process"],"value_type":"int","is_vector":false,"nullable":false,"default":0,"min":0,"label":"Raft layers","category":"Support","ui_level":"simple",
   "gui":{"tab":"process","page":"Support","group":"Raft"},"scopes":["object"],"cli_flags":["--raft-layers"]},
  {"key":"outer_wall_speed","owner":"process","preset_types":["process"],"value_type":"float","is_vector":true,"nullable":true,"default":[60],"min":1,"max":1000,
   "label":"Outer wall","sidetext":"mm/s","category":"Speed","ui_level":"simple","gui":{"tab":"process","page":"Speed","group":"Other layers speed"},"scopes":["object","part"],"cli_flags":["--outer-wall-speed"]},
  {"key":"nozzle_temperature","owner":"filament","preset_types":["filament"],"value_type":"int","is_vector":true,"nullable":false,"default":[200],"min":0,"max":500,
   "label":"Nozzle","sidetext":"degC","ui_level":"simple","gui":{"tab":"filament","page":"Filament","group":"Print temperature"},"cli_flags":["--nozzle-temperature"]},
  {"key":"machine_max_speed_x","owner":"printer","preset_types":["printer"],"value_type":"float","is_vector":true,"nullable":false,"default":[500,200],"min":0,
   "full_label":"Maximum speed X","sidetext":"mm/s","category":"Machine limits","ui_level":"simple","gui":{"tab":"printer","page":"Motion ability","group":"Speed limitation"},
   "creality_vendor_policy":"read_only","cli_flags":["--machine-max-speed-x"]},
  {"key":"thumbnails","owner":"printer","preset_types":["printer"],"value_type":"string","is_vector":false,"nullable":false,"default":"","label":"G-code thumbnails","ui_level":"advanced",
   "gui":{"tab":"printer","page":"Basic information","group":"Advanced"},"creality_vendor_policy":"hidden","cli_flags":["--thumbnails"]},
  {"key":"gcode_flavor","owner":"printer","preset_types":["printer"],"value_type":"enum","is_vector":false,"nullable":false,"default":"klipper",
   "enum":{"values":["marlin","klipper"],"labels":["Marlin","Klipper"]},"label":"G-code flavor","ui_level":"advanced","gui":{"tab":"printer","page":"Basic information","group":"Advanced"},"cli_flags":["--gcode-flavor"]},
  {"key":"printable_area","owner":"printer","preset_types":["printer"],"value_type":"point","is_vector":true,"nullable":false,"default":[[0,0],[260,0],[260,260],[0,260]],
   "label":"Printable area","ui_level":"advanced","gui":{"tab":"printer","page":"Basic information","group":"Printable space"},"cli_flags":["--printable-area"]},
  {"key":"spiral_mode","owner":"process","preset_types":["process"],"value_type":"bool","is_vector":false,"nullable":false,"default":false,"label":"Spiral vase","category":"Others","ui_level":"simple",
   "gui":{"tab":"process","page":"Others","group":"Special mode"},"scopes":["plate"],"cli_flags":["--spiral-mode"]},
  {"key":"curr_bed_type","owner":"plate","preset_types":[],"value_type":"enum","is_vector":false,"nullable":false,"default":"Textured PEI Plate",
   "enum":{"values":["Textured PEI Plate","Cool Plate"]},"label":"Bed type","ui_level":"simple","gui":{"tab":"plate","page":"Plate Settings","group":""},"scopes":["plate"]},
  {"key":"print_host","owner":"printer","preset_types":["printer"],"value_type":"string","is_vector":false,"nullable":false,"default":"","label":"Hostname","ui_level":"advanced","nocli":true},
  {"key":"secret_dev_switch","owner":"process","preset_types":["process"],"value_type":"bool","is_vector":false,"nullable":false,"default":false,"label":"Developer switch","ui_level":"develop",
   "gui":{"tab":"process","page":"Others","group":"Special mode"},"cli_flags":["--secret-dev-switch"]},
  {"key":"sparse_infill_pattern","owner":"process","preset_types":["process"],"value_type":"enum","is_vector":false,"nullable":false,"default":"grid",
   "enum":{"values":["grid","gyroid","zig-zag"],"labels":["Grid","Gyroid","Rectilinear"]},"label":"Sparse infill pattern","category":"Strength","ui_level":"simple",
   "gui":{"tab":"process","page":"Strength","group":"Infill"},"scopes":["object","part"],"cli_flags":["--sparse-infill-pattern"],
   "gui_enum_restrictions":[{"when":"ai_infill is enabled","values":["grid","zig-zag"]}],
   "gated_by":[{"effect":"line","constant":"false"}]}
 ]
}`

func fixture(t *testing.T) *Catalog {
	t.Helper()
	c, err := FromJSON([]byte(fixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeTexts is a TextSource with invented wording.
type fakeTexts map[uint64]string

func (f fakeTexts) Text(h uint64) (string, bool) { s, ok := f[h]; return s, ok }
