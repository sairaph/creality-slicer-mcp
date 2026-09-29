package profiles

import "testing"

func TestEvalCondition(t *testing.T) {
	cfg := map[string]any{
		"printer_model":    "Creality K2",
		"printer_variant":  "0.4",
		"nozzle_diameter":  []string{"0.4", "0.6"},
		"printer_notes":    "PRINTER_VENDOR_CREALITY\nPRINTER_MODEL_K2",
		"num_extruders":    "1",
		"gcode_flavor":     "klipper",
		"empty":            "",
		"single_in_a_list": []string{"x"},
		"nozzle_volume":    "183",
	}
	for expr, want := range map[string]bool{
		`printer_model == "Creality K2"`:                                                   true,
		`printer_model == 'Creality K2'`:                                                   true,
		`printer_model != "Creality K2"`:                                                   false,
		`printer_model == "Creality K2 Plus"`:                                              false,
		`nozzle_diameter[0] == 0.4`:                                                        true,
		`nozzle_diameter[1] == 0.4`:                                                        false,
		`nozzle_diameter[1] > 0.5 and nozzle_diameter[0] < 0.5`:                            true,
		`printer_variant == "0.4"`:                                                         true,
		`printer_variant == 0.4`:                                                           true,
		`nozzle_volume >= 183`:                                                             true,
		`nozzle_volume > 183`:                                                              false,
		`printer_notes=~/.*PRINTER_VENDOR_CREALITY.*/`:                                     true,
		`printer_notes =~ /printer_model_k2/i`:                                             true,
		`printer_notes =~ /printer_model_k2/`:                                              false,
		`printer_notes !~ /VENDOR_QIDI/`:                                                   true,
		`not (printer_model == "x")`:                                                       true,
		`!(printer_model == "Creality K2")`:                                                false,
		`printer_model == "a" or printer_variant == "0.4"`:                                 true,
		`printer_model == "a" || printer_variant == "0.4"`:                                 true,
		`printer_model == "Creality K2" && printer_variant == "0.4"`:                       true,
		`(printer_model == "a" or printer_variant == "0.4") and gcode_flavor == "klipper"`: true,
		`printer_model == "a" and printer_variant == "0.4" or gcode_flavor == "klipper"`:   true,
		`true`:                          true,
		`false or true`:                 true,
		`num_extruders == 1`:            true,
		`empty == ""`:                   true,
		`single_in_a_list[0] == "x"`:    true,
		`printer_model == "say \"hi\""`: false,
	} {
		got, err := EvalCondition(expr, cfg)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
}

func TestEvalConditionErrors(t *testing.T) {
	cfg := map[string]any{"a": "1", "v": []string{"1", "2"}}
	for _, expr := range []string{
		``,
		`unknown_var == 1`,
		`a ==`,
		`a == "x" and`,
		`(a == "x"`,
		`a == "x")`,
		`v == 1`,     // a vector needs an index
		`v[5] == 1`,  // out of range
		`a[1] == 1`,  // a scalar has no element 1
		`a =~ "x"`,   // regex must be /.../
		`a =~ /(/`,   // bad regex
		`f(1) == 2`,  // functions are not supported
		`a + 1 == 2`, // arithmetic is not supported
		`"unterminated`,
		`a`,         // not a boolean
		`1 == "x"`,  // number against non numeric text
		`true == 1`, // bool against number
		`not a`,     // not of a non boolean
		`a == 1 == 1`,
	} {
		if v, err := EvalCondition(expr, cfg); err == nil {
			t.Errorf("%q must fail, got %v", expr, v)
		}
	}
}
