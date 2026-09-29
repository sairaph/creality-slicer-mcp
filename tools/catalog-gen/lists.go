package main

// lists.go: preset key lists (Preset.cpp, PresetBundle.cpp, PrintConfig.cpp, Tab.cpp)
// and the static config classes of PrintConfig.hpp.

import (
	"regexp"
	"strings"
)

type KeyItem struct {
	Key     string
	Line    int
	Cond    string
	Section string // nearest preceding pure comment line inside the list, if any
}

type KeyList struct {
	Name  string
	File  string
	Line  int
	Items []KeyItem
}

func (l *KeyList) Has(key string) bool {
	if l == nil {
		return false
	}
	for _, it := range l.Items {
		if it.Key == key {
			return true
		}
	}
	return false
}

func (l *KeyList) Keys() []string {
	var out []string
	if l == nil {
		return out
	}
	for _, it := range l.Items {
		out = append(out, it.Key)
	}
	return out
}

var (
	reKeyListDecl = regexp.MustCompile(`(?s)^(?:static\s+)?(?:const\s+)?std::(?:vector|set)<std::string>\s+(\w+)\s*(?:=\s*)?\{(.*)\}$`)
	reKeyListAsg  = regexp.MustCompile(`(?s)^(m_\w+_keys)\s*=\s*\{(.*)\}$`)
)

func parseKeyLists(ct *CText, root *Node) map[string]*KeyList {
	out := map[string]*KeyList{}
	var visit func(n *Node)
	visit = func(n *Node) {
		for _, c := range n.Children {
			if c.Kind == nBlock {
				visit(c)
				continue
			}
			var name, body string
			if m := reKeyListDecl.FindStringSubmatch(c.Text); m != nil {
				name, body = m[1], m[2]
			} else if m := reKeyListAsg.FindStringSubmatch(c.Text); m != nil {
				name, body = m[1], m[2]
			} else {
				continue
			}
			kl := &KeyList{Name: name, File: ct.Rel, Line: c.Line}
			bodyStart := strings.Index(c.Text, "{")
			for _, lit := range stringLiterals(c.Text) {
				if lit.Off < bodyStart {
					continue
				}
				line := ct.lineAt(c.Off + lit.Off)
				kl.Items = append(kl.Items, KeyItem{Key: lit.Val, Line: line, Cond: ct.condAt(line), Section: sectionComment(ct, c.Line, line)})
			}
			_ = body
			out[name] = kl
		}
	}
	visit(root)
	return out
}

// StaticClass is one PRINT_CONFIG_CLASS_DEFINE from PrintConfig.hpp.
type StaticClass struct {
	Name    string
	Parents []string
	Members []StaticMember
	Line    int
}

type StaticMember struct {
	Key     string
	CppType string
	Line    int
}

var reMember = regexp.MustCompile(`\(\(\s*(ConfigOption[\w:<>]*)\s*,\s*(\w+)\s*\)\)`)

func parseStaticClasses(ct *CText) (map[string]*StaticClass, []string) {
	out := map[string]*StaticClass{}
	var order []string
	text := string(ct.Buf)
	re := regexp.MustCompile(`(?m)^\s*PRINT_CONFIG_CLASS_(DERIVED_)?DEFINE(0)?\(`)
	for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
		open := loc[1] - 1
		end := matchParen(text, open)
		if end < 0 {
			continue
		}
		args := splitTop(text[open+1:end], ',')
		if len(args) < 2 {
			continue
		}
		derived := loc[2] >= 0
		sc := &StaticClass{Name: strings.TrimSpace(args[0]), Line: ct.lineAt(open)}
		seq := ""
		if derived {
			par := strings.TrimSpace(args[1])
			par = strings.TrimSuffix(strings.TrimPrefix(par, "("), ")")
			for _, p := range strings.Split(par, ",") {
				if p = strings.TrimSpace(p); p != "" {
					sc.Parents = append(sc.Parents, p)
				}
			}
			if len(args) > 2 {
				seq = strings.Join(args[2:], ",")
			}
		} else {
			seq = strings.Join(args[1:], ",")
		}
		seqBase := strings.Index(text[open:end], seq)
		for _, m := range reMember.FindAllStringSubmatchIndex(seq, -1) {
			off := open + max(seqBase, 0) + m[0]
			sc.Members = append(sc.Members, StaticMember{
				CppType: seq[m[2]:m[3]],
				Key:     seq[m[4]:m[5]],
				Line:    ct.lineAt(off),
			})
		}
		// sanity: number of "((" openings should equal parsed members
		if strings.Count(seq, "((") != len(sc.Members) {
			warnf("%s:%d: static class %s: %d '((' groups but %d members parsed", ct.Rel, sc.Line, sc.Name, strings.Count(seq, "(("), len(sc.Members))
		}
		out[sc.Name] = sc
		order = append(order, sc.Name)
	}
	return out, order
}

// allKeys returns the members of a class including those of its parents.
func allKeys(classes map[string]*StaticClass, name string) map[string]StaticMember {
	res := map[string]StaticMember{}
	sc := classes[name]
	if sc == nil {
		return res
	}
	for _, par := range sc.Parents {
		for k, v := range allKeys(classes, par) {
			res[k] = v
		}
	}
	for _, m := range sc.Members {
		res[m.Key] = m
	}
	return res
}

var reCommentLine = regexp.MustCompile(`^\s*//+\s*(.*?)\s*$`)

// sectionComment returns the nearest pure "// text" line at or above `line`
// (but not above `first`), which in Preset.cpp key lists names a provenance
// section such as "// Creality" or "// BBS".
func sectionComment(ct *CText, first, line int) string {
	for l := line; l >= first && l >= 1 && l-1 < len(ct.Raw); l-- {
		if m := reCommentLine.FindStringSubmatch(ct.Raw[l-1]); m != nil {
			return m[1]
		}
	}
	return ""
}
