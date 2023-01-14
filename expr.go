package cuerious

import (
	"bytes"
	"fmt"
	"math/bits"

	"cuelang.org/go/cue"
	"github.com/xlab/treeprint"
)

// ExprNode is the node element from which an ExprTree is constructed.
//
// Each ExprNode which corresponds to exactly one [cue.Value], stored in ExprNode.self.
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
	// The cue.Value of this node in the expression tree.
	self cue.Value

	// The op produced from calling Expr() on self.
	// TODO remove, can just call Expr
	op cue.Op
	// Child nodes returned from calling self.Expr()
	children []*ExprNode

	// The default value for this node. nil if there is no default.
	dfault *ExprNode
	// Indicator that the node is attached to the tree through a default link.
	fromDefault bool

	// The underlying value to which self is a reference. nil if self is not a reference.
	ref *ExprNode
	// Path to the referenced value
	// TODO remove, can just call ReferencePath
	refpath cue.Path
	// Indicator that this ExprNode is part of the tree through a reference edge
	fromDeref bool

	depth int
}

type arc struct {
	Parent, Child *ExprNode
}

// ExprTree constructs a tree-ish of nodes representing the expression structure
// contained within the provided [cue.Value].
//
// ExprTree is represented as a set of interlinked pointers to ExprNode, each of
// which corresponds to exactly one [cue.Value], ExprNode.Self. ExprNodes are connected through
// one of three types of links:
//   - Expr links, where calling [cue.Value.Expr] on the
//   - Dereference links, where the parentl
//
// Structural CUE elements - like the fields of structs - are NOT represented in
// this tree.
//
// Returns nil if the provided value contains structural cycles.
func ExprTree(v cue.Value) *ExprNode {
	if v.Validate(cue.DisallowCycles(true)) != nil {
		return nil
	}
	return exprTree(v, -1)
}

func exprTree(v cue.Value, depth int) *ExprNode {
	op, args := v.Expr()
	dv, has := v.Default()
	_, path := v.ReferencePath()

	n := &ExprNode{
		op:    op,
		self:  v,
		depth: depth + 1,
	}

	var doargs, dodefault bool
	switch v.IncompleteKind() {
	case cue.ListKind:
		dodefault = has && !v.Equals(dv)
		doargs = op != cue.NoOp || dodefault
	case cue.StructKind:
		doargs = op != cue.NoOp || has
		dodefault = has
	default:
		doargs = op != cue.NoOp || has
		dodefault = has
	}

	if dodefault {
		n.dfault = exprTree(dv, n.depth)
		n.dfault.parent = n
		n.dfault.fromDefault = true
	}

	if len(path.Selectors()) > 0 {
		n.ref = exprTree(cue.Dereference(v), n.depth)
		n.refpath = path
		n.ref.parent = n
		n.ref.fromDeref = true
	}

	if doargs {
		for _, cv := range args {
			cn := exprTree(cv, n.depth)
			cn.parent = n
			n.children = append(n.children, cn)
		}
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
	tp := treeprint.NewWithRoot(n.printSelf())
	n.treeprint(tp)
	return tp.String()
}

func (n *ExprNode) treeprint(tp treeprint.Tree) {
	if n.isLeaf() {
		if !n.isRoot() {
			tp.AddNode(n.printSelf())
		}
		return
	}

	var b treeprint.Tree
	if n.isRoot() {
		b = tp
		tp.SetMetaValue(n.opString())
	} else {
		b = tp.AddMetaBranch(n.opString(), n.printSelf())
	}

	for _, cn := range n.children {
		cn.treeprint(b)
	}
	if n.ref != nil {
		// n.ref.treeprint(b.AddMetaBranch(fmt.Sprintf("ref:%s", n.refpath), n.ref.kindStr()))
		n.ref.treeprint(b.AddMetaBranch(fmt.Sprintf("ref:%s", n.refpath), ""))
	}

	if n.dfault != nil {
		n.dfault.treeprint(b.AddMetaBranch("*", ""))
	}
}

func (n *ExprNode) printSelf() string {
	return fmt.Sprintf("%s%s", n.kindStr(), n.attrStr())
}

func (n *ExprNode) opString() string {
	return n.op.String()
}

func (n *ExprNode) isRoot() bool {
	return n.parent == nil && !n.fromDeref && !n.fromDefault
}

func (n *ExprNode) isLeaf() bool {
	return n.ref == nil && n.dfault == nil && len(n.children) == 0
}

func (n *ExprNode) kindStr() string {
	switch n.self.Kind() {
	case cue.BottomKind, cue.StructKind, cue.ListKind:
		ik := n.self.IncompleteKind()
		if bits.OnesCount16(uint16(ik)) != 1 {
			return ik.String()
		}
		if ik != cue.ListKind {
			return fmt.Sprintf("(%s)", ik.String())
		}

		l := n.self.Len()
		if l.IsConcrete() {
			return "(olist)"
		} else {
			return "(clist)"
		}
	default:
		str := fmt.Sprint(n.self)
		if len(str) < 12 {
			return str
		}
		return str[:12] + "..."
	}
}

func (n *ExprNode) attrStr() string {
	attrs := n.self.Attributes(cue.ValueAttr)
	var buf bytes.Buffer
	for _, attr := range attrs {
		fmt.Fprintf(&buf, " @%s(%s)", attr.Name(), attr.Contents())
	}
	return buf.String()
}
