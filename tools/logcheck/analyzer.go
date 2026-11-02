// Package logcheck is a go/analysis pass that enforces the S12 logging rules the type system cannot:
//
//  1. slog messages are constant strings (no interpolated personal data; details go in attributes);
//  2. message content is never logged: []byte, MIME parts and mail messages are refused outside the
//     protocol-log packages (internal/protolog, internal/mailproto/…), which mask and cap them.
//
// A finding can be silenced on its line with a justification: //logcheck:allow <reason>.
package logcheck

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer is the logcheck pass.
var Analyzer = &analysis.Analyzer{
	Name: "logcheck",
	Doc:  "checks slog calls for constant messages and refuses message content (S12 §3, §11)",
	Run:  run,
}

// msgIndex maps slog function/method names to the index of their message argument.
var msgIndex = map[string]int{
	"Debug": 0, "Info": 0, "Warn": 0, "Error": 0,
	"DebugContext": 1, "InfoContext": 1, "WarnContext": 1, "ErrorContext": 1,
	"Log": 2, "LogAttrs": 2,
}

// contentPkgs are packages whose types carry message content.
var contentPkgs = []string{"github.com/emersion/go-message", "net/mail", "mime/multipart", "net/textproto"}

// protocolPkgs may log raw bytes (they mask credentials and cap sizes themselves).
var protocolPkgs = []string{"/internal/protolog", "/internal/mailproto/", "/internal/logging"}

func run(pass *analysis.Pass) (any, error) {
	pkgPath := pass.Pkg.Path()
	allowBytes := false
	for _, p := range protocolPkgs {
		if strings.Contains(pkgPath+"/", p) {
			allowBytes = true
		}
	}
	for _, file := range pass.Files {
		fname := pass.Fset.File(file.Pos()).Name()
		if strings.HasSuffix(fname, "_test.go") {
			continue
		}
		allowed := allowedLines(pass, file)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn := calledSlogFunc(pass, call)
			if fn == "" {
				return true
			}
			line := pass.Fset.Position(call.Pos()).Line
			if allowed[line] {
				return true
			}
			if idx, ok := msgIndex[fn]; ok && idx < len(call.Args) {
				msg := call.Args[idx]
				if tv, ok := pass.TypesInfo.Types[msg]; ok && tv.Value == nil {
					pass.Reportf(msg.Pos(), "log message must be a constant string; put variable data in attributes (S12 §3)")
				}
				for _, a := range call.Args[idx+1:] {
					checkContent(pass, a, allowBytes)
				}
			}
			if fn == "Any" && len(call.Args) == 2 {
				checkContent(pass, call.Args[1], allowBytes)
			}
			return true
		})
	}
	return nil, nil
}

// calledSlogFunc returns the name of the log/slog function or *slog.Logger method called, or "".
func calledSlogFunc(pass *analysis.Pass, call *ast.CallExpr) string {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.Ident:
		id = f
	default:
		return ""
	}
	obj, ok := pass.TypesInfo.Uses[id].(*types.Func)
	if !ok || obj.Pkg() == nil || obj.Pkg().Path() != "log/slog" {
		return ""
	}
	return obj.Name()
}

func checkContent(pass *analysis.Pass, e ast.Expr, allowBytes bool) {
	tv, ok := pass.TypesInfo.Types[e]
	if !ok || tv.Type == nil {
		return
	}
	t := tv.Type
	if s, ok := t.Underlying().(*types.Slice); ok && !allowBytes {
		if b, ok := s.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
			pass.Reportf(e.Pos(), "do not log raw bytes (message content); log logging.Size(key, len(b)) instead (S12 §11)")
			return
		}
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil {
		path := named.Obj().Pkg().Path()
		for _, cp := range contentPkgs {
			if path == cp || strings.HasPrefix(path, cp+"/") {
				pass.Reportf(e.Pos(), "do not log %s.%s: it carries message content (S12 §11)", path, named.Obj().Name())
				return
			}
		}
	}
}

func allowedLines(pass *analysis.Pass, f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(c.Text, "//")), "logcheck:allow ") {
				out[pass.Fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}
