package flamegraph

import "sort"

// node represents a frame in the flame graph tree.
type node struct {
	name     string
	value    int64 // Total value including all descendants.
	children []*node
}

// findChild returns the child with the given name, or nil.
func (n *node) findChild(name string) *node {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

// sortChildren sorts children alphabetically for deterministic layout.
func (n *node) sortChildren() {
	sort.Slice(n.children, func(i, j int) bool {
		return n.children[i].name < n.children[j].name
	})
	for _, c := range n.children {
		c.sortChildren()
	}
}

// maxDepth returns the maximum depth of the tree (0-indexed).
func (n *node) maxDepth() int {
	max := 0
	for _, c := range n.children {
		d := 1 + c.maxDepth()
		if d > max {
			max = d
		}
	}
	return max
}

// buildTree merges folded stacks into a frame tree.
// The root node represents "all" with the total value.
func buildTree(stacks []FoldedStack) *node {
	if len(stacks) == 0 {
		return nil
	}

	root := &node{name: "root"}
	for _, s := range stacks {
		if len(s.Frames) == 0 || s.Value <= 0 {
			continue
		}
		root.value += s.Value
		cur := root
		for _, frame := range s.Frames {
			child := cur.findChild(frame)
			if child == nil {
				child = &node{name: frame}
				cur.children = append(cur.children, child)
			}
			child.value += s.Value
			cur = child
		}
	}

	if root.value == 0 {
		return nil
	}

	root.sortChildren()
	return root
}

// layoutNode holds computed layout coordinates for rendering.
type layoutNode struct {
	name     string
	value    int64
	x        float64 // X offset in value units.
	depth    int
	children []*layoutNode
}

// layoutTree computes x positions for all nodes in the tree.
func layoutTree(n *node) *layoutNode {
	ln := &layoutNode{
		name:  n.name,
		value: n.value,
		x:     0,
		depth: 0,
	}
	layoutChildren(ln, n, 0, 0)
	return ln
}

// layoutChildren recursively assigns x positions and depths.
func layoutChildren(ln *layoutNode, n *node, x float64, depth int) {
	childX := x
	for _, c := range n.children {
		cl := &layoutNode{
			name:  c.name,
			value: c.value,
			x:     childX,
			depth: depth + 1,
		}
		ln.children = append(ln.children, cl)
		layoutChildren(cl, c, childX, depth+1)
		childX += float64(c.value)
	}
}
