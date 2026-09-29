// catalog-gen builds a settings catalog of Creality Print from its C++ source.
//
//	go run . -repo <path-to-repo> -ref v7.2.1                 # writes ./settings-catalog-7.2.1.json
//	go run . -repo <path-to-repo> -ref v7.2.1 -slim <out>     # slim catalog for internal/catalog
//	go run . -diff old.json new.json [-diff-out d.json]       # compares two catalogs
//	go run . -sample catalog.json -n 15 -seed 1               # stratified sample for hand checks
//	go run . -tree catalog.json                               # GUI layout tree (markdown)
//	go run . -deps catalog.json                               # which options gate which (markdown)
//
// Source files are read with `git show <ref>:<path>`; the repository is never
// checked out or modified, and nothing touches the network.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	fPrintConfigCpp = "src/libslic3r/PrintConfig.cpp"
	fPrintConfigHpp = "src/libslic3r/PrintConfig.hpp"
	fPresetCpp      = "src/libslic3r/Preset.cpp"
	fPresetBundle   = "src/libslic3r/PresetBundle.cpp"
	fTabCpp         = "src/slic3r/GUI/Tab.cpp"
	fConfigManip    = "src/slic3r/GUI/ConfigManipulation.cpp"
	fUserModeJSON   = "resources/profiles/CrealityUserMode.json"
	fProcessCfgJSON = "resources/images/process/ProcessConfig.json"
)

func main() {
	repo := flag.String("repo", "", "path to the CrealityPrint git repository")
	ref := flag.String("ref", "", "git tag or ref to read (e.g. v7.2.1)")
	out := flag.String("out", "", "output path of the full catalog JSON (default ./settings-catalog-<ref>.json unless -slim is given)")
	slim := flag.String("slim", "", "output path of the slim catalog embedded by internal/catalog (no tooltip text, tooltip_hash only)")
	diff := flag.Bool("diff", false, "compare two catalogs: -diff old.json new.json")
	diffOut := flag.String("diff-out", "", "write the full machine-readable diff here")
	sample := flag.String("sample", "", "print a stratified random sample of a catalog file")
	tree := flag.String("tree", "", "print the GUI layout tree of a catalog file (markdown)")
	deps := flag.String("deps", "", "print the gating drivers of a catalog file (markdown)")
	n := flag.Int("n", 15, "sample size")
	seed := flag.Int64("seed", 1, "sample seed")
	flag.Parse()

	switch {
	case *diff:
		if flag.NArg() != 2 {
			fatalf("usage: -diff old.json new.json")
		}
		if err := runDiff(flag.Arg(0), flag.Arg(1), *diffOut); err != nil {
			fatalf("%v", err)
		}
	case *tree != "":
		if err := runTree(*tree); err != nil {
			fatalf("%v", err)
		}
	case *deps != "":
		if err := runDeps(*deps); err != nil {
			fatalf("%v", err)
		}
	case *sample != "":
		if err := runSample(*sample, *n, *seed); err != nil {
			fatalf("%v", err)
		}
	default:
		if *repo == "" || *ref == "" {
			fatalf("usage: go run . -repo <path> -ref <tag> [-out file]")
		}
		if *out == "" && *slim == "" {
			*out = "settings-catalog-" + strings.TrimPrefix(*ref, "v") + ".json"
		}
		if err := generate(*repo, *ref, *out, *slim); err != nil {
			fatalf("%v", err)
		}
	}
}

func fatalf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

func generate(repo, ref, outPath, slimPath string) error {
	src, err := newGitSource(repo, ref)
	if err != nil {
		return err
	}
	fmt.Printf("ref %s -> commit %s\n", ref, src.Commit)

	load := func(rel string) (*CText, *Node, error) {
		data, err := src.Read(rel)
		if err != nil {
			hint := ""
			if alt := src.Find(filepath.Base(rel)); len(alt) > 0 {
				hint = " (found elsewhere: " + strings.Join(alt, ", ") + ")"
			}
			return nil, nil, fmt.Errorf("%s at %s: %v%s", rel, ref, err, hint)
		}
		ct, err := loadClean(rel, data)
		if err != nil {
			return nil, nil, err
		}
		return ct, buildTree(ct), nil
	}

	cfgCt, cfgTree, err := load(fPrintConfigCpp)
	if err != nil {
		return err
	}
	hppCt, _, err := load(fPrintConfigHpp)
	if err != nil {
		return err
	}
	presetCt, presetTree, err := load(fPresetCpp)
	if err != nil {
		return err
	}
	bundleCt, bundleTree, err := load(fPresetBundle)
	if err != nil {
		return err
	}
	tabCt, tabTree, err := load(fTabCpp)
	if err != nil {
		return err
	}
	cmCt, cmTree, err := load(fConfigManip)
	if err != nil {
		return err
	}

	b := &Build{
		Ref: ref, Commit: src.Commit,
		CfgCt: cfgCt, HppCt: hppCt, TabCt: tabCt, CmCt: cmCt, PresetCt: presetCt, BundleCt: bundleCt,
	}

	// --- definitions
	b.EnumMaps = parseEnumMaps(cfgCt, cfgTree)
	dp := newDefParser(cfgCt, b.EnumMaps)
	dp.consts = src.Constants([]*CText{cfgCt, hppCt})
	dp.walkDefs(cfgTree)
	b.Defs = dp

	// --- key lists
	b.Lists = map[string]*KeyList{}
	for _, pair := range []struct {
		ct   *CText
		tree *Node
	}{{presetCt, presetTree}, {bundleCt, bundleTree}, {cfgCt, cfgTree}, {tabCt, tabTree}} {
		for name, kl := range parseKeyLists(pair.ct, pair.tree) {
			b.Lists[name] = kl
		}
	}
	b.Classes, b.ClassOrder = parseStaticClasses(hppCt)

	// --- GUI
	gp := newGuiParser(tabCt, tabTree)
	gp.runTab("process", "global process preset (also the base of the per-object/part/layer tabs)", "TabPrint::build")
	gp.runTab("filament", "filament preset", "TabFilament::build")
	gp.runTab("printer", "printer preset", "TabPrinter::build_fff")
	gp.runTab("plate", "per-plate override tab", "TabPrintPlate::build")
	gp.runTab("object_frequent", "extra page of the per-object/part/layer tabs", "TabPrintModel::build")
	b.Gui = gp

	// --- gating
	gpTab := newGuiParser(tabCt, tabTree) // function index only
	cmFuncs := newGuiParser(cmCt, cmTree)
	b.warnings = append(b.warnings, walkGates(tabCt, gpTab.funcs, []string{"TabPrint::toggle_options", "TabFilament::toggle_options", "TabPrinter::toggle_options", "Tab::update_support_options_visibility"}, &b.Gates, &b.Forced)...)
	b.warnings = append(b.warnings, walkGates(cmCt, cmFuncs.funcs, []string{"ConfigManipulation::toggle_print_fff_options", "ConfigManipulation::update_print_fff_config", "ConfigManipulation::toggle_print_sla_options"}, &b.Gates, &b.Forced)...)
	b.EnumRestr = enumRestrictions(tabCt, gpTab.funcs, b.EnumMaps, dp)

	// --- resources
	b.UserMode = map[string]string{}
	if data, err := src.Read(fUserModeJSON); err == nil {
		b.parseUserMode(data)
	} else {
		b.warnings = append(b.warnings, fmt.Sprintf("%s not readable: %v", fUserModeJSON, err))
	}
	if data, err := src.Read(fProcessCfgJSON); err == nil {
		b.parseProcessCfg(data)
	} else {
		b.warnings = append(b.warnings, fmt.Sprintf("%s not readable: %v", fProcessCfgJSON, err))
	}

	cat := b.assemble()

	if outPath != "" {
		size, err := writeJSON(outPath, cat, " ")
		if err != nil {
			return err
		}
		b.printReport(cat, outPath, size, src)
	} else {
		b.printReport(cat, "(full catalog not written)", 0, src)
	}
	if slimPath != "" {
		sf := b.slim(cat)
		size, err := writeJSON(slimPath, sf, "")
		if err != nil {
			return err
		}
		fmt.Println("wrote slim catalog", slimPath, size, "bytes,", len(sf.Options), "options")
	}
	return nil
}

// writeJSON encodes v (indented when indent is not empty) and writes it. Literal
// em and en dashes are written as JSON unicode escapes so no file contains them.
func writeJSON(path string, v interface{}, indent string) (int, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return 0, err
	}
	bs := byte(92) // backslash
	data := bytes.ReplaceAll(buf.Bytes(), []byte(string(rune(0x2014))), []byte{bs, 'u', '2', '0', '1', '4'})
	data = bytes.ReplaceAll(data, []byte(string(rune(0x2013))), []byte{bs, 'u', '2', '0', '1', '3'})
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	return len(data), os.WriteFile(path, data, 0o644)
}
