package main

// source.go: read-only access to the source tree of one git ref. Files are
// read with `git show <ref>:<path>`; nothing is checked out or modified.

import (
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

type GitSource struct {
	Repo   string
	Ref    string
	Commit string
}

func (g *GitSource) run(args ...string) ([]byte, error) {
	full := append([]string{"-C", g.Repo}, args...)
	cmd := exec.Command("git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("git %s: timed out", strings.Join(args, " "))
	}
	return out.Bytes(), nil
}

func newGitSource(repo, ref string) (*GitSource, error) {
	g := &GitSource{Repo: repo, Ref: ref}
	out, err := g.run("rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return nil, err
	}
	g.Commit = strings.TrimSpace(string(out))
	return g, nil
}

// Read returns the content of a path at the ref.
func (g *GitSource) Read(rel string) ([]byte, error) {
	return g.run("show", g.Ref+":"+rel)
}

// Exists reports whether a path exists at the ref.
func (g *GitSource) Exists(rel string) bool {
	out, err := g.run("ls-tree", "--name-only", g.Ref, "--", rel)
	return err == nil && strings.TrimSpace(string(out)) != ""
}

var (
	reInclude = regexp.MustCompile(`(?m)^\s*#\s*include\s+"([^"]+)"`)
	reConst   = regexp.MustCompile(`(?:inline\s+)?(?:static\s+)?(?:constexpr|const)\s+(?:double|float|int|size_t|unsigned(?:\s+int)?)\s+(\w+)\s*=\s*([^;]+);`)
)

// Constants collects simple numeric constants from the headers directly
// included by the given files (used to resolve defaults such as
// ZAA_DEFAULT_SLICE_PLANE_OFFSET_MM). Best effort: unresolved names are ignored.
func (g *GitSource) Constants(from []*CText) map[string]float64 {
	type def struct{ name, expr string }
	var defs []def
	seen := map[string]bool{}
	for _, ct := range from {
		for _, m := range reInclude.FindAllStringSubmatch(strings.Join(ct.Raw, "\n"), -1) {
			for _, cand := range []string{path.Join("src/libslic3r", m[1]), path.Join("src", m[1]), path.Join("src/libslic3r", path.Base(m[1]))} {
				if seen[cand] {
					break
				}
				data, err := g.Read(cand)
				if err != nil {
					continue
				}
				seen[cand] = true
				text := string(stripComments(data))
				for _, c := range reConst.FindAllStringSubmatch(text, -1) {
					defs = append(defs, def{c[1], strings.TrimSpace(c[2])})
				}
				break
			}
		}
	}
	// also constants defined in the from-files themselves
	for _, ct := range from {
		for _, c := range reConst.FindAllStringSubmatch(string(ct.Buf), -1) {
			defs = append(defs, def{c[1], strings.TrimSpace(c[2])})
		}
	}
	res := map[string]float64{}
	tmp := &DefParser{consts: res}
	env := &denv{nums: map[string]float64{}, strs: map[string]string{}, loop: map[string]string{}}
	for pass := 0; pass < 6; pass++ {
		for _, d := range defs {
			if _, ok := res[d.name]; ok {
				continue
			}
			if v, ok := tmp.evalNum(d.expr, env); ok {
				res[d.name] = v
			}
		}
	}
	return res
}

// Find lists paths at the ref whose base name equals name (used only to suggest
// a location when an expected file has moved).
func (g *GitSource) Find(name string) []string {
	out, err := g.run("ls-tree", "-r", "--name-only", g.Ref)
	if err != nil {
		return nil
	}
	var res []string
	for _, l := range strings.Split(string(out), "\n") {
		if l == name || strings.HasSuffix(l, "/"+name) {
			res = append(res, l)
		}
	}
	return res
}
