package cuerious

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"github.com/xlab/treeprint"
)

// ExprNode is the node element from which an ExprTree is constructed.
//
// Each ExprNode which corresponds to exactly one [cue.Value], stored in ExprNode.Self.
// ExprNodes contain connections to each other through one of three types of links:
//   - Expr link: the child is one element of a larger CUE expression represented by the
//     parent (returns from [cue.Value.Expr] on parent).
//   - Default link: the child is a default value of the parent (returns from [cue.Value.Default])
//   - Dereference link: the parent is a reference to the child (returns from [cue.Dereference])
type ExprNode struct {
	// The parent node - backlink up the tree. The contained cue.Value in that node either:
	//   - Returns this node (and possibly others) when its Expr() method is called.
	//   - Is the return from calling dfault() on the parent cue.Value
	//   - Is the return from calling cue.Dereference() on the parent cue.Value
	parent *ExprNode
	// The cue.Value corresponding to this node in the expression tree.
	V cue.Value

	// The op produced from calling Expr() on Self.
	// TODO remove, can just call Expr
	op cue.Op
	// Child nodes returned from calling Self.Expr()
	children []*ExprNode

	// The default value for this node. nil if there is no default.
	dfault *ExprNode
	// Indicator that the node is attached to the tree through a default link.
	fromDefault bool

	// The underlying value to which Self is a reference. nil if Self is not a reference.
	ref *ExprNode
	// Path to the referenced value
	// TODO remove, can just call ReferencePath
	refpath cue.Path
	// Indicator that this ExprNode is part of the tree through a reference edge
	fromDeref bool

	// Internal traacker of the universe of known cue.Value in this whole tree/graph.
	m map[cue.Value]*ExprNode
}

// ExprTree constructs a tree-ish of nodes representing the expression structure
// contained within the provided [cue.Value].
//
// ExprTree is represented as a set of interlinked pointers to ExprNode, each of
// which corresponds to exactly one [cue.Value], ExprNode.Self. ExprNodes are
// connected through one of three types of links:
//   - Expr links, where calling [cue.Value.Expr] on the
//   - Dereference links, where the parentl
//
// Structural CUE elements - like the fields of structs - are NOT represented in
// this tree.
//
// An ExprNode structure is NOT currently safe for use from multiple goroutines.
//
// Returns nil if the provided value contains structural cycles.
func ExprTree(v cue.Value) *ExprNode {
	if v.Validate(cue.DisallowCycles(true)) != nil {
		return nil
	}
	return exprTree(v, make(map[cue.Value]*ExprNode))
}

func exprTree(v cue.Value, m map[cue.Value]*ExprNode) *ExprNode {
	if n, has := m[v]; has {
		return n
	}

	op, args := v.Expr()
	dv, hasDef := v.Default()
	_, path := v.ReferencePath()

	n := &ExprNode{
		op: op,
		V:  v,
	}

	var doargs, dodefault bool
	switch v.IncompleteKind() {
	case cue.ListKind:
		dodefault = hasDef && !v.Equals(dv) && v != dv
		doargs = op != cue.NoOp || dodefault
	case cue.StructKind:
		doargs = op != cue.NoOp || hasDef
		dodefault = hasDef
	default:
		if len(path.Selectors()) == 0 {
			doargs = op != cue.NoOp || hasDef
			dodefault = hasDef
		}
	}

	if dodefault {
		n.dfault = exprTree(dv, n.m)
		n.dfault.parent = n
		n.dfault.fromDefault = true
	}

	if len(path.Selectors()) > 0 {
		n.ref = exprTree(cue.Dereference(v), n.m)
		n.refpath = path
		n.ref.parent = n
		n.ref.fromDeref = true
	}

	if doargs {
		for _, cv := range args {
			cn := exprTree(cv, n.m)
			cn.parent = n
			n.children = append(n.children, cn)
		}
		sort.Slice(n.children, func(i, j int) bool {
			return n.children[i].op < n.children[j].op
		})
	}

	return n
}

// Walk does a pre-order traversal of the tree, calling the provided walk
// function for each visited ExprNode. If the function returns false for
// any node, children of that node are not visited.
func (n *ExprNode) Walk(fn func(x *ExprNode) bool) {
	if !fn(n) {
		return
	}

	if n.ref != nil {
		n.ref.Walk(fn)
	}
	for _, c := range n.children {
		c.Walk(fn)
	}

	if n.dfault != nil {
		n.dfault.Walk(fn)
	}
}

func (n *ExprNode) String() string {
	tp := treeprint.NewWithRoot(n.selfString())
	n.treeprint(tp)
	return tp.String()
}

func (n *ExprNode) treeprint(tp treeprint.Tree) {
	if n.isLeaf() {
		tp.AddNode(n.selfString())
	}

	if n.dfault != nil {
		tp2 := tp.AddMetaBranch(n.opString(), n.selfString())
		n.dfault.treeprint(tp2)
	}
	if n.ref != nil {
		tp2 := tp.AddMetaBranch(n.opString(), n.selfString())
		n.ref.treeprint(tp2)
	}

	if len(n.children) > 1 {
		tp = tp.AddMetaBranch(n.op.String(), n.selfString())
	}
	for _, cn := range n.children {
		cn.treeprint(tp)
	}
}

func (n *ExprNode) selfString() string {
	strs := make([]string, 0, 3)
	for _, str := range []string{n.kindStr(), n.valStr(), n.attrStr()} {
		if len(str) > 0 {
			strs = append(strs, str)
		}
	}

	return strings.Join(strs, " ")
}

func (n *ExprNode) opString() string {
	switch {
	case n.ref != nil:
		return "ref"
	case n.dfault != nil:
		return "*"
	default:
		return n.op.String()
	}
}

func (n *ExprNode) isRoot() bool {
	return n.parent == nil && !n.fromDeref && !n.fromDefault
}

func (n *ExprNode) isLeaf() bool {
	return n.ref == nil && n.dfault == nil && len(n.children) == 0
}

func (n *ExprNode) valStr() string {
	b := new(strings.Builder)
	if n.fromDefault {
		fmt.Fprint(b, "*")
	}

	switch {
	case n.ref != nil:
		fmt.Fprint(b, n.refpath.String())
	case n.dfault != nil:
		return ""
	default:
		switch n.V.Kind() {
		case cue.BottomKind:
			// Ugh, Kind() of a list with non-concrete elements is bottom
			if n.V.IncompleteKind() != cue.ListKind {
				return ""
			}
			fallthrough
		case cue.ListKind:
			fmt.Fprintf(b, "[%s]", strings.TrimPrefix(fmt.Sprint(n.V.Len()), "int & "))
		case cue.StructKind:
			fmt.Fprint(b, "{")
			// TODO Len()'s docs say it reports a value for structs, but apparently not?
			// fmt.Fprint(b, n.Self.Len())
			if n.V.Allows(cue.AnyString) {
				fmt.Fprint(b, "...")
			}
			fmt.Fprint(b, "}")
		default:
			str := fmt.Sprint(n.V)
			if len(str) > 12 {
				str = str[:12] + "..."
			}
			fmt.Fprint(b, str)
		}
	}

	return b.String()
}

func (n *ExprNode) kindStr() string {
	var v cue.Value
	switch {
	case n.ref != nil:
		v = n.ref.V
	case n.dfault != nil:
		v = n.dfault.V
	default:
		v = n.V
	}

	var pk func(pv cue.Value) string
	pk = func(pv cue.Value) string {
		b := new(strings.Builder)
		switch ik := pv.IncompleteKind(); ik {
		default:
			return fmt.Sprintf("<%s>", ik)
		case cue.StructKind:
			// fmt.Fprintf(b, "<%s%s>", ik, pk(pv.LookupPath(cue.MakePath(cue.AnyString))))
			fmt.Fprintf(b, "<%s", ik)
			if av := pv.LookupPath(cue.MakePath(cue.AnyString)); av.Exists() {
				fmt.Fprint(b, pk(av))
			}
			fmt.Fprint(b, ">")
		case cue.ListKind:
			// fmt.Fprintf(b, "<%s%s>", ik, pk(pv.LookupPath(cue.MakePath(cue.AnyIndex))))
			fmt.Fprintf(b, "<%s", ik)
			if av := pv.LookupPath(cue.MakePath(cue.AnyIndex)); av.Exists() {
				fmt.Fprint(b, pk(av))
			}
			fmt.Fprint(b, ">")
		}
		return b.String()
	}

	return pk(v)
}

func (n *ExprNode) attrStr() string {
	attrs := n.V.Attributes(cue.ValueAttr)
	var buf bytes.Buffer
	for _, attr := range attrs {
		fmt.Fprintf(&buf, " @%s(%s)", attr.Name(), attr.Contents())
	}
	return buf.String()
}
