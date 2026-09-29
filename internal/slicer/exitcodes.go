package slicer

import "fmt"

// ExitCode is one entry of the CLI exit code table
// (v7.2.1:src/libslic3r/Utils.hpp, dev_docs/10-cli.md sections 9 and 15.7).
type ExitCode struct {
	Code    int32
	Name    string
	Meaning string
	Hint    string
}

// exitCodes holds every code Creality Print 7.2 can return. Codes are the
// signed values a shell shows; a Windows process reports them as unsigned
// (4294967296 + code), which Runner converts back.
var exitCodes = []ExitCode{
	{-1, "ENVIRONMENT_ERROR", "the slicer could not set up its environment or load its libraries", "Reinstall Creality Print, or check that the installation folder is complete."},
	{-2, "INVALID_PARAMS", "the command line was rejected (bad option or value, plate number out of range, missing output folder, or an unsupported input combination)", "This is normally a request the tool built wrongly; check the plate number and the input files, and report the slicer message."},
	{-3, "FILE_NOTFOUND", "an input, preset or custom G-code file does not exist", "Check that every input path is absolute and the file exists."},
	{-4, "FILELIST_INVALID_ORDER", "the project 3MF was not the first input", "Pass the project 3MF before any other input file."},
	{-5, "CONFIG_FILE_ERROR", "a preset file could not be used (unreadable JSON, unknown from or type, duplicate or missing machine or process, filament count mismatch)", "Check the flattened preset files: one machine and one process at most, valid JSON, one filament per used slot."},
	{-6, "DATA_FILE_ERROR", "a model file failed to load", "Check that the model is a valid STL, OBJ or 3MF and is not empty or corrupt."},
	{-7, "INVALID_PRINTER_TECH", "the printer is not an FDM printer", "Use an FDM printer preset."},
	{-8, "UNSUPPORTED_OPERATION", "the requested command is not supported", "Report this: the tool should only ever ask for a slice."},
	{-9, "COPY_OBJECTS_ERROR", "copying objects failed", "Report this; it should be unreachable."},
	{-10, "SCALE_TO_FIT_ERROR", "scaling to fit failed", "Report this; it should be unreachable."},
	{-11, "EXPORT_STL_ERROR", "exporting an STL failed", "Report this: the tool should never export STL."},
	{-12, "EXPORT_OBJ_ERROR", "exporting an OBJ failed", "Report this; it should be unreachable."},
	{-13, "EXPORT_3MF_ERROR", "writing the sliced 3MF failed", "Report this: the tool should never export a 3MF through the CLI."},
	{-14, "OUT_OF_MEMORY", "the slicer ran out of memory", "Close other programs, or slice a smaller model."},
	{-15, "3MF_NOT_SUPPORT_MACHINE_CHANGE", "the 3MF does not support a printer change", "Report this; it should be unreachable."},
	{-16, "3MF_NEW_MACHINE_NOT_SUPPORTED", "the 3MF is not compatible with the new printer", "Use a printer preset the project supports."},
	{-17, "PROCESS_NOT_COMPATIBLE", "the process (quality) preset does not list this printer as compatible", "Choose a process preset that is compatible with the printer preset."},
	{-18, "INVALID_VALUES_IN_3MF", "the merged settings failed validation (a value is out of range or inconsistent)", "Check the changed settings and the printer, process and filament combination; the slicer message names the value."},
	{-19, "POSTPROCESS_NOT_SUPPORTED", "the 3MF contains post-processing scripts, which the CLI refuses", "Remove the post-processing script from the project."},
	{-20, "PRINTABLE_SIZE_REDUCED", "the printable area shrank", "Report this; it should be unreachable."},
	{-21, "OBJECT_ARRANGE_FAILED", "arranging the objects failed", "Try fewer or smaller objects, or place them manually."},
	{-22, "OBJECT_ORIENT_FAILED", "auto-orienting the objects failed", "Slice without auto-orient."},
	{-23, "MODIFIED_PARAMS_TO_PRINTER", "the project bed is larger than the reference printer", "Report this; it should be unreachable."},
	{-24, "FILE_VERSION_NOT_SUPPORTED", "the 3MF was saved by a newer version than this slicer", "Re-save the project with this version, or allow newer files."},
	{-50, "NO_SUITABLE_OBJECTS", "nothing to slice: the plate is empty or no object is fully inside the bed", "Check that the plate has objects and that they are inside the printable area."},
	{-51, "VALIDATE_ERROR", "the slicer's own validation of the print failed", "Read the slicer message; it names the setting or object that is invalid."},
	{-52, "OBJECTS_PARTLY_INSIDE", "an object crosses the edge of the bed", "Move the object fully inside the printable area."},
	{-53, "EXPORT_CACHE_DIRECTORY_CREATE_FAILED", "creating the slice cache folder failed", "Report this: the tool should not use slice caches."},
	{-54, "EXPORT_CACHE_WRITE_FAILED", "writing the slice cache failed", "Report this: the tool should not use slice caches."},
	{-55, "IMPORT_CACHE_NOT_FOUND", "the slice cache was not found", "Report this: the tool should not use slice caches."},
	{-56, "IMPORT_CACHE_DATA_CAN_NOT_USE", "the slice cache cannot be used", "Report this: the tool should not use slice caches."},
	{-57, "IMPORT_CACHE_LOAD_FAILED", "loading the slice cache failed", "Report this: the tool should not use slice caches."},
	{-58, "SLICING_TIME_EXCEEDS_LIMIT", "slicing took longer than the allowed time", "Report this: the tool sets no slicing time limit."},
	{-59, "TRIANGLE_COUNT_EXCEEDS_LIMIT", "the model has more triangles than allowed", "Report this: the tool sets no triangle limit."},
	{-60, "NO_SUITABLE_OBJECTS_AFTER_SKIP", "skipping the requested objects left nothing to slice on a plate", "Skip fewer objects."},
	{-61, "FILAMENT_NOT_MATCH_BED_TYPE", "a filament is not allowed on the plate's bed type", "Choose another bed type, or another filament."},
	{-62, "FILAMENTS_DIFFERENT_TEMP", "the used filaments need temperatures too far apart to print together", "Use filaments with closer print temperatures."},
	{-63, "OBJECT_COLLISION_IN_SEQ_PRINT", "objects collide when printed one after another", "Space the objects further apart, or print all at once by layer."},
	{-64, "OBJECT_COLLISION_IN_LAYER_PRINT", "objects collide when printed layer by layer", "Space the objects further apart."},
	{-65, "SPIRAL_MODE_INVALID_PARAMS", "vase (spiral) mode has invalid parameters", "Vase mode needs a single object without infill or supports."},
	{-100, "SLICING_ERROR", "an error while slicing or writing the G-code (empty layers, overlapping objects or a preset problem)", "Read the slicer message; try a different layer height, or check the model for very thin parts."},
	{-101, "GCODE_PATH_CONFLICTS", "the generated G-code has path conflicts between objects", "Space the objects further apart."},
}

var exitCodeByValue = func() map[int32]ExitCode {
	m := make(map[int32]ExitCode, len(exitCodes))
	for _, e := range exitCodes {
		m[e.Code] = e
	}
	return m
}()

// LookupExitCode returns the table entry for a CLI exit code.
func LookupExitCode(code int32) (ExitCode, bool) {
	e, ok := exitCodeByValue[code]
	return e, ok
}

// ExitCodes returns a copy of the table.
func ExitCodes() []ExitCode { return append([]ExitCode(nil), exitCodes...) }

// ntstatusNames names the native crash codes a Windows process can exit with.
var ntstatusNames = map[uint32]string{
	0xC0000005: "EXCEPTION_ACCESS_VIOLATION",
	0xC000001D: "EXCEPTION_ILLEGAL_INSTRUCTION",
	0xC0000094: "EXCEPTION_INT_DIVIDE_BY_ZERO",
	0xC00000FD: "EXCEPTION_STACK_OVERFLOW",
	0xC0000409: "STATUS_STACK_BUFFER_OVERRUN",
	0xC0000374: "STATUS_HEAP_CORRUPTION",
	0xC0000135: "STATUS_DLL_NOT_FOUND",
	0xC0000142: "STATUS_DLL_INIT_FAILED",
	0xC0000017: "STATUS_NO_MEMORY",
	0xC000013A: "STATUS_CONTROL_C_EXIT",
	0xC0000096: "EXCEPTION_PRIV_INSTRUCTION",
	0xC0000008: "STATUS_INVALID_HANDLE",
}

// CrashName returns the NTSTATUS name of an exit code that is a native crash
// (0xC0000005 -> EXCEPTION_ACCESS_VIOLATION). Unnamed 0xC0000000-range codes
// are reported as "NTSTATUS 0x...". ok is false for ordinary exit codes.
func CrashName(code int32) (name string, ok bool) {
	u := uint32(code)
	if n, found := ntstatusNames[u]; found {
		return n, true
	}
	if u&0xF0000000 == 0xC0000000 {
		return fmt.Sprintf("NTSTATUS 0x%08X", u), true
	}
	return "", false
}

// codes73 are the exit codes Creality Print 7.3 defines
// (v7.3.0:src/libslic3r/Utils.hpp:26-64, messages in src/slic3r/CLI/CLIError.cpp).
// 7.3 dropped the transform and export codes of 7.2 (-9 to -13, -15, -16, -20,
// -22, -23); every other code has the same meaning, so the 7.2 wording is reused.
var codes73 = []int32{-1, -2, -3, -4, -5, -6, -7, -8, -14, -17, -18, -19, -21, -24,
	-50, -51, -52, -53, -54, -55, -56, -57, -58, -59, -60, -61, -62, -63, -64, -65, -100, -101}

var exitCodeByValue73 = func() map[int32]ExitCode {
	m := make(map[int32]ExitCode, len(codes73))
	for _, c := range codes73 {
		e := exitCodeByValue[c]
		switch c {
		case -2:
			e.Meaning = "the command line was rejected (bad option or value, plate number out of range, missing output folder, or an unsupported input combination)"
			e.Hint = "This is normally a request the tool built wrongly; check the plate number and the input file, and report the slicer message."
		case -4, -5:
			continue
		}
		m[c] = e
	}
	// -4 and -5 keep the 7.2 meaning.
	m[-4], m[-5] = exitCodeByValue[-4], exitCodeByValue[-5]
	return m
}()

// LookupExitCode73 returns the table entry of a Creality Print 7.3 exit code.
func LookupExitCode73(code int32) (ExitCode, bool) {
	e, ok := exitCodeByValue73[code]
	return e, ok
}
