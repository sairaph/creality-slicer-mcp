package slicer

// CrashHint is the hint for a slice that crashed (D-V73-1): Creality Print
// 7.2.2 crashes natively when it slices a plate that uses two or more filaments
// and prints by layer, while 7.3 slices the same project and printing by object
// works on both. It returns "" for every other case. filaments is the number
// of different filaments the plate uses and printSequence its print sequence
// ("by layer" or "by object").
func CrashHint(dialect string, o Outcome, filaments int, printSequence string) string {
	if dialect != "v72" || o.Code != OutcomeCrashed || filaments < 2 || printSequence == "by object" {
		return ""
	}
	return "Creality Print 7.2.2 crashes when slicing multi-filament plates printed by layer; update to 7.3 or set print_sequence to by object for this plate"
}
