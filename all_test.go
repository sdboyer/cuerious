package cuerious_test

import (
	"fmt"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	"github.com/sdboyer/cuerious"
	"github.com/sdboyer/cuerious/internal/cuetxtar"
)

// func TestMain(m *testing.M) {
// 	os.Exit(testscript.RunMain(m, map[string]func() int{}))
// }

func TestDumpExprTree(t *testing.T) {
	test := cuetxtar.TxTarTest{
		Root: "testdata",
		Name: "exprtree",
	}

	test.Run(t, func(tc *cuetxtar.Test) {
		ctx := cuecontext.New()
		v := ctx.BuildInstance(tc.Instance())
		if v.Err() != nil {
			tc.Fatalf("errors in cue input: %s", v.Err())
		}
		iter, err := v.Fields(cue.All())
		if err != nil {
			tc.Fatal(err)
		}

		for iter.Next() {
			w := tc.Writer(iter.Selector().String())
			n := cuerious.ExprTree(iter.Value())
			sb, err := format.Node(n.V.Source())
			if err != nil {
				t.Fatalf("could not format node: %s", err)
			}
			fmt.Fprintf(w, "%s\n\n", string(sb))
			fmt.Fprintf(w, "%s\n\n", n.String())
		}
	})
}
