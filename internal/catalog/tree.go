package catalog

import "strings"

// Node is one level of the GUI layout: the root, a tab, a page or a group.
type Node struct {
	Name     string    // segment name ("Quality"); empty for the root
	Path     string    // "process/Quality"
	Kind     string    // root, tab, page or group
	Count    int       // settings below this node after filtering
	Children []*Node   // tabs, pages or groups in GUI order (empty beyond maxLevel)
	Options  []*Option // set on group nodes that are expanded
}

var kinds = []string{"root", "tab", "page", "group"}

// Tree returns the layout node at pathPrefix ("" for the root) expanded maxLevel
// levels down: 0 gives the node with its count only, 1 its children, 2 the
// grandchildren and so on; a group node that is expanded lists its settings.
// Settings without a GUI line are not part of the tree. The order is the GUI
// order of the application.
func (c *Catalog) Tree(pathPrefix string, maxLevel int) *Node {
	return c.TreeFiltered(pathPrefix, maxLevel, Filter{})
}

// TreeFiltered is Tree restricted to the settings the filter allows (its
// PathPrefix field is ignored; use the pathPrefix argument).
func (c *Catalog) TreeFiltered(pathPrefix string, maxLevel int, f Filter) *Node {
	f.PathPrefix = ""
	root := &Node{Kind: "root"}
	index := map[string]*Node{}
	for _, o := range c.opts {
		if o.GUI == nil || !f.allows(o) {
			continue
		}
		segs := []string{o.GUI.Tab, o.GUI.Page}
		if o.GUI.Group != "" {
			segs = append(segs, o.GUI.Group)
		}
		parent := root
		path := ""
		for i, s := range segs {
			if path == "" {
				path = s
			} else {
				path += "/" + s
			}
			n := index[path]
			if n == nil {
				n = &Node{Name: s, Path: path, Kind: kinds[i+1]}
				index[path] = n
				parent.Children = append(parent.Children, n)
			}
			n.Count++
			parent = n
		}
		root.Count++
		parent.Options = append(parent.Options, o)
	}
	start := root
	if segs := splitPath(pathPrefix); len(segs) > 0 {
		cur := root
		for _, s := range segs {
			var next *Node
			for _, ch := range cur.Children {
				if strings.EqualFold(ch.Name, s) {
					next = ch
					break
				}
			}
			if next == nil {
				return nil
			}
			cur = next
		}
		start = cur
	}
	return prune(start, maxLevel)
}

func prune(n *Node, depth int) *Node {
	out := &Node{Name: n.Name, Path: n.Path, Kind: n.Kind, Count: n.Count}
	if depth <= 0 {
		return out
	}
	out.Options = n.Options
	for _, ch := range n.Children {
		out.Children = append(out.Children, prune(ch, depth-1))
	}
	return out
}
