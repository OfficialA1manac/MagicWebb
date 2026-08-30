package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Teardown order in run() is a correctness invariant that cannot be observed
// from a unit test at runtime — exercising it would need a live Postgres, a
// live RPC endpoint and a bound listener. It is, however, entirely visible in
// the source, so this test asserts it there: it parses main.go, isolates the
// body of run(), and checks the ordering of the teardown calls.
//
// The invariant: cancel() must run BEFORE the drains, and every sink the
// indexer / keepers / collection verifier write through (the audit log queue,
// the RPC pool) must close AFTER those writers have drained. Closing a sink
// first silently loses the writer's final rows.

type runBody struct {
	fset *token.FileSet
	fn   *ast.FuncDecl
}

func parseRun(t *testing.T) runBody {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Recv == nil && fd.Name.Name == "run" && fd.Body != nil {
			return runBody{fset: fset, fn: fd}
		}
	}
	t.Fatal("func run() not found in main.go — the teardown lives there")
	return runBody{}
}

// callPos returns the source offset of the sole call to sel ("al.Close",
// "cancel", ...) inside run(), ignoring calls that are the target of a defer
// statement. It fails the test when the call is missing or ambiguous.
func (r runBody) callPos(t *testing.T, sel string) int {
	t.Helper()
	var found []int
	deferred := map[ast.Node]bool{}
	ast.Inspect(r.fn.Body, func(n ast.Node) bool {
		if d, ok := n.(*ast.DeferStmt); ok && d.Call != nil {
			deferred[d.Call] = true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || deferred[call] {
			return true
		}
		if callName(call.Fun) == sel {
			found = append(found, r.fset.Position(call.Pos()).Offset)
		}
		return true
	})
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("run() no longer calls %s() — teardown wiring was removed", sel)
	default:
		t.Fatalf("run() calls %s() %d times; this test assumes exactly one", sel, len(found))
	}
	return 0
}

// recvPos returns the source offset of the receive on the named channel.
func (r runBody) recvPos(t *testing.T, ch string) int {
	t.Helper()
	pos := -1
	ast.Inspect(r.fn.Body, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.ARROW {
			return true
		}
		if id, ok := u.X.(*ast.Ident); ok && id.Name == ch {
			if p := r.fset.Position(u.Pos()).Offset; pos < 0 || p < pos {
				pos = p
			}
		}
		return true
	})
	if pos < 0 {
		t.Fatalf("run() never waits on <-%s — that goroutine can be cut off mid-write", ch)
	}
	return pos
}

func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

func TestTeardownOrder(t *testing.T) {
	r := parseRun(t)

	cancel := r.callPos(t, "cancel")
	indexer := r.recvPos(t, "indexerDone")
	verifier := r.recvPos(t, "verifierDone")
	alClose := r.callPos(t, "al.Close")
	ethClose := r.callPos(t, "eth.Close")
	election := r.callPos(t, "keeperElection.Shutdown")

	lastDrain := indexer
	if verifier > lastDrain {
		lastDrain = verifier
	}

	if cancel > indexer || cancel > verifier {
		t.Error("cancel() must precede the indexer and verifier drains; " +
			"waiting on a goroutine whose context is still live hangs until the deadline")
	}
	for _, sink := range []struct {
		name string
		pos  int
	}{
		{"al.Close()", alClose},
		{"eth.Close()", ethClose},
		{"keeperElection.Shutdown()", election},
	} {
		if sink.pos < lastDrain {
			t.Errorf("%s runs before the indexer/verifier drains complete — "+
				"writers are still live and their final writes are lost", sink.name)
		}
	}
}

// TestNoFatalAfterResourceAcquisition guards the other half of the fix:
// log.Fatal calls os.Exit(1), which skips every deferred teardown in run()
// (DB pools, RPC transports, and — worst case — the keeper's Postgres
// advisory lock, which then stays held until its session times out and
// blocks the next leader). Failures inside run() must return an error so
// main() can log and exit after the defers have run.
func TestNoFatalAfterResourceAcquisition(t *testing.T) {
	r := parseRun(t)
	ast.Inspect(r.fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if callName(call.Fun) == "log.Fatal" {
			t.Errorf("log.Fatal at %s: run() must return an error instead so deferred teardown runs",
				r.fset.Position(call.Pos()))
		}
		return true
	})
}
