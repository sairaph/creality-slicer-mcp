// Package flush computes the purge (flush) volume matrix of a multi-filament
// project: how much filament is pushed through the nozzle when the printer
// switches from one filament to another, as a function of the two colours and
// of the printer's nozzle volume. The MCP stores the raw matrix itself
// (dev_docs/22-implementation-plan.md, B3), so it must reproduce what Creality
// Print computes from colours. The pair volumes use float32, like the slicer;
// the minimum volumes follow the GUI rule (see MinVolumes).
package flush

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Fixed constants of the volume formula.
const (
	minPairVolume   = 60   // smallest pair volume before the nozzle volume is added
	maxPairVolume   = 1200 // largest pair volume
	toSupportVolume = 230  // value when the next filament is a support filament
	fromSupportMin  = 700  // smallest value when the previous filament is a support filament
	filamentDiam    = 1.75 // mm, used to turn a retraction length into a volume
)

// Config is a flattened config: every value is a string (scalar) or a
// []string (vector), which is exactly what profiles.Preset.Values holds, so a
// merged printer plus filament map can be passed as it is.
type Config map[string]any

func (c Config) scalar(key string) (string, bool) {
	switch v := c[key].(type) {
	case string:
		return v, true
	case []string:
		if len(v) > 0 {
			return v[0], true
		}
	}
	return "", false
}

func (c Config) vector(key string) []string {
	switch v := c[key].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []string:
		return v
	}
	return nil
}

func (c Config) number(key string) (float64, bool) {
	s, ok := c.scalar(key)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

func vectorNumber(v []string, i int) (float64, bool) {
	if i < 0 || i >= len(v) {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v[i]), 64)
	if err != nil || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// nilFlag is the value a "nil" (unset) entry of a nullable boolean vector has
// in the application: neither 0 nor 1.
const nilFlag = 2

// flagAt reads entry i of a boolean vector the way the application does: an
// entry that is missing (the whole vector absent or too short) counts as 0, a
// "nil" entry as nilFlag, anything else as its integer value.
func flagAt(v []string, i int) int {
	if i < 0 || i >= len(v) {
		return 0
	}
	s := strings.TrimSpace(v[i])
	if strings.EqualFold(s, "nil") {
		return nilFlag
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) {
		return nilFlag
	}
	return int(f)
}

// MinVolumes returns min_flush[i] for every filament of the config: the
// nozzle volume minus the volume that a long retraction already pulls out.
// It follows the rule of the application's GUI (Plater.cpp get_min_flush_volumes,
// the same in 7.2 and 7.3 for a single nozzle), which is what a project saved
// by the GUI holds:
//
//   - The printer retracts long when enable_long_retraction_when_cut is not 0
//     and long_retractions_when_cut[0] is 1; the length is then the printer's
//     retraction_distances_when_cut[0] cut to a whole number of millimetres.
//     Otherwise the starting length is 0.
//   - A filament whose long-retraction flag is 0 resets the length to 0. A flag
//     of 1 with printer level 2 (per filament mode) takes the filament's own
//     distance (whole millimetres), or the printer's when that is unset or NaN.
//     Any other flag, including an unset ("nil") entry, keeps the starting
//     length: an unset filament follows the printer.
//   - A missing flag vector counts as all zeros.
//   - min_flush[i] = int(nozzle_volume - PI*1.75*1.75/4 * length), in double
//     arithmetic with truncation toward zero.
//
// The number of filaments is the length of filament_colour (else
// filament_type, else filament_long_retractions_when_cut), at least 1. It
// returns an error when nozzle_volume is missing or not positive.
func MinVolumes(cfg Config) ([]int, error) {
	n := 0
	for _, key := range []string{"filament_colour", "filament_type", "filament_long_retractions_when_cut"} {
		if n = len(cfg.vector(key)); n > 0 {
			break
		}
	}
	if n == 0 {
		n = 1
	}
	nozzle, ok := cfg.number("nozzle_volume")
	if !ok || nozzle <= 0 {
		return nil, fmt.Errorf("flush: the config has no positive nozzle_volume (the printer preset supplies it)")
	}
	level := 0
	if v, ok := cfg.number("enable_long_retraction_when_cut"); ok {
		level = int(v)
	}
	machineOn := flagAt(cfg.vector("long_retractions_when_cut"), 0) == 1
	printerDist := 18.0 // the application's default when the printer gives none
	if d, ok := vectorNumber(cfg.vector("retraction_distances_when_cut"), 0); ok {
		printerDist = d
	}
	start := 0
	if level != 0 && machineOn {
		start = int(printerDist)
	}
	flags := cfg.vector("filament_long_retractions_when_cut")
	dists := cfg.vector("filament_retraction_distances_when_cut")
	area := math.Pi * filamentDiam * filamentDiam / 4
	out := make([]int, n)
	for i := range out {
		retract := start
		switch flag := flagAt(flags, i); {
		case flag == 0:
			retract = 0
		case flag == 1 && level == 2:
			if d, ok := vectorNumber(dists, i); ok {
				retract = int(d)
			} else {
				retract = int(printerDist)
			}
		}
		out[i] = int(float64(int(nozzle)) - area*float64(retract))
	}
	return out, nil
}

// Matrix returns the N x N flush matrix, row-major: element N*src+dst is the
// volume (mm3) to purge when switching from filament src to filament dst.
// colours are "#RRGGBB" or "#RRGGBBAA" (alpha 0 counts as white), minFlush
// comes from MinVolumes (nil means zeros) and isSupport marks support
// filaments (nil means none). The diagonal is 0; a switch to a support
// filament is 230; a switch from a support filament is at least 700.
func Matrix(colours []string, minFlush []int, isSupport []bool) ([]int, error) {
	n := len(colours)
	if minFlush != nil && len(minFlush) != n {
		return nil, fmt.Errorf("flush: %d colours but %d min flush volumes", n, len(minFlush))
	}
	if isSupport != nil && len(isSupport) != n {
		return nil, fmt.Errorf("flush: %d colours but %d support flags", n, len(isSupport))
	}
	cols := make([]rgb, n)
	for i, c := range colours {
		v, err := parseColour(c)
		if err != nil {
			return nil, fmt.Errorf("flush: filament %d: %w", i+1, err)
		}
		cols[i] = v
	}
	m := make([]int, n*n)
	for src := 0; src < n; src++ {
		for dst := 0; dst < n; dst++ {
			if src == dst {
				continue
			}
			min := 0
			if minFlush != nil {
				min = minFlush[src]
			}
			var v int
			if isSupport != nil && isSupport[dst] {
				v = toSupportVolume
			} else {
				v = pairVolume(cols[src], cols[dst], min)
			}
			if isSupport != nil && isSupport[src] && v < fromSupportMin {
				v = fromSupportMin
			}
			m[n*src+dst] = v
		}
	}
	return m, nil
}

type rgb struct{ r, g, b float32 }

// parseColour reads #RRGGBB or #RRGGBBAA; alpha 0 is treated as white.
func parseColour(s string) (rgb, error) {
	s = strings.TrimSpace(s)
	if len(s) != 7 && len(s) != 9 || s[0] != '#' {
		return rgb{}, fmt.Errorf("colour %q is not #RRGGBB or #RRGGBBAA", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("colour %q is not #RRGGBB or #RRGGBBAA", s)
	}
	if len(s) == 9 {
		if v&0xff == 0 {
			return rgb{1, 1, 1}, nil
		}
		v >>= 8
	}
	return rgb{float32(v>>16&0xff) / 255, float32(v>>8&0xff) / 255, float32(v&0xff) / 255}, nil
}

// hsv converts to hue in degrees (0..360), saturation and value (0..1).
func (c rgb) hsv() (h, s, v float32) {
	max := f32max(c.r, f32max(c.g, c.b))
	min := f32min(c.r, f32min(c.g, c.b))
	d := max - min
	v = max
	if max > 0 {
		s = d / max
	}
	if d == 0 {
		return 0, s, v
	}
	switch max {
	case c.r:
		h = 60 * float32(math.Mod(float64((c.g-c.b)/d), 6))
	case c.g:
		h = 60 * ((c.b-c.r)/d + 2)
	default:
		h = 60 * ((c.r-c.g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return h, s, v
}

func (c rgb) luminance() float32 { return 0.3*c.r + 0.59*c.g + 0.11*c.b }

func f32max(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func f32min(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func cos32(x float32) float32  { return float32(math.Cos(float64(x))) }
func sin32(x float32) float32  { return float32(math.Sin(float64(x))) }
func sqrt32(x float32) float32 { return float32(math.Sqrt(float64(x))) }

// pairVolume is the flush volume from src to dst colour, plus the source
// filament's min flush volume.
func pairVolume(src, dst rgb, minFlush int) int {
	const rad = float32(math.Pi / 180)
	h1, s1, v1 := src.hsv()
	h2, s2, v2 := dst.hsv()
	dx := cos32(h1*rad)*s1*v1 - cos32(h2*rad)*s2*v2
	dy := sin32(h1*rad)*s1*v1 - sin32(h2*rad)*s2*v2
	hs := f32min(1.2, sqrt32(dx*dx+dy*dy))

	ls, ld := src.luminance(), dst.luminance()
	var lumi float32
	if ld >= ls {
		lumi = float32(math.Pow(float64(ld-ls), 0.7)) * 560
	} else {
		lumi = (ls - ld) * 80
		hs = f32min(hs, 0.67*v2+0.33*v1)
	}
	hsf := 230 * hs
	vol := sqrt32(hsf*hsf + lumi*lumi - 2*hsf*lumi*cos32(120*rad))
	vol = f32max(vol, minPairVolume)
	vol += float32(minFlush)
	out := int(vol)
	if out > maxPairVolume {
		out = maxPairVolume
	}
	return out
}
