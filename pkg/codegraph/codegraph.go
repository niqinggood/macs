// Package codegraph builds a knowledge graph from source code.
// Inspired by codebase-memory-mcp: tree-sitter AST → knowledge graph.
// Currently supports Go via stdlib go/ast (zero deps).
package codegraph

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Symbol represents a code entity extracted from source.
type Symbol struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"` // func, method, type, interface, const, var
	Package  string   `json:"package"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Exported bool     `json:"exported"`
	Doc      string   `json:"doc,omitempty"`
	Calls    []string `json:"calls,omitempty"`    // functions/methods called
	Methods  []string `json:"methods,omitempty"`  // for interfaces
	Receiver string   `json:"receiver,omitempty"` // for methods
	Fields   []string `json:"fields,omitempty"`   // for structs
}

// ImportLink represents a package import relationship.
type ImportLink struct {
	SourcePkg string `json:"source_pkg"`
	TargetPkg string `json:"target_pkg"`
	Alias     string `json:"alias,omitempty"`
}

// Graph contains all extracted symbols and relationships.
type Graph struct {
	Symbols  []Symbol     `json:"symbols"`
	Imports  []ImportLink `json:"imports"`
	Files    int          `json:"files"`
	Packages []string     `json:"packages"`
}

// ScanOptions configures codebase scanning.
type ScanOptions struct {
	Root       string   // root directory to scan
	Exclude    []string // directories to exclude
	MaxFiles   int      // max files to parse (0 = unlimited)
	GoOnly     bool     // only scan Go files
}

// DefaultExcludeDirs lists directories commonly excluded from scanning.
var DefaultExcludeDirs = []string{
	".git", "node_modules", "__pycache__", ".venv", "venv", "vendor",
	"dist", "build", ".next", "coverage", "target", ".openkitty",
}

// ScanGoProject scans a Go project and builds a code graph.
func ScanGoProject(root string) (*Graph, error) {
	return Scan(ScanOptions{
		Root:    root,
		Exclude: DefaultExcludeDirs,
		GoOnly:  true,
		MaxFiles: 10000,
	})
}

// Scan walks a directory tree and extracts symbols from source files.
func Scan(opts ScanOptions) (*Graph, error) {
	if opts.Root == "" {
		opts.Root = "."
	}
	if len(opts.Exclude) == 0 {
		opts.Exclude = DefaultExcludeDirs
	}

	g := &Graph{}
	pkgSet := make(map[string]bool)

	// Use go/parser to parse the entire package tree
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, opts.Root, func(info os.FileInfo) bool {
		// Skip excluded dirs
		for _, excl := range opts.Exclude {
			if strings.Contains(info.Name(), excl) {
				return false
			}
		}
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)

	if err != nil {
		// If ParseDir fails, try walking individual files
		return scanGoFiles(opts)
	}

	for pkgName, pkg := range pkgs {
		pkgSet[pkgName] = true
		for fileName, file := range pkg.Files {
			g.Files++
			extractGoFile(g, file, pkgName, fileName, fset)
		}
	}

	for pkg := range pkgSet {
		g.Packages = append(g.Packages, pkg)
	}
	return g, nil
}

// scanGoFiles is a fallback when ParseDir fails (e.g., mixed Go versions).
func scanGoFiles(opts ScanOptions) (*Graph, error) {
	g := &Graph{}
	pkgSet := make(map[string]bool)
	fset := token.NewFileSet()

	filepath.Walk(opts.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			for _, excl := range opts.Exclude {
				if info != nil && strings.Contains(info.Name(), excl) {
					return filepath.SkipDir
				}
			}
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		g.Files++
		pkgSet[file.Name.Name] = true
		extractGoFile(g, file, file.Name.Name, path, fset)
		return nil
	})

	for pkg := range pkgSet {
		g.Packages = append(g.Packages, pkg)
	}
	return g, nil
}

// extractGoFile extracts symbols from a single Go file AST.
func extractGoFile(g *Graph, file *ast.File, pkgName, fileName string, fset *token.FileSet) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sym := extractFuncDecl(d, pkgName, fileName, fset)
			g.Symbols = append(g.Symbols, sym)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					g.Symbols = append(g.Symbols, extractTypeSpec(s, pkgName, fileName, fset))
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() || true { // include all for completeness
							g.Symbols = append(g.Symbols, Symbol{
								Name:     name.Name,
								Kind:     "var",
								Package:  pkgName,
								File:     fileName,
								Line:     fset.Position(name.Pos()).Line,
								Exported: name.IsExported(),
							})
						}
					}
				}
			}
		}
	}

	// Extract imports
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		g.Imports = append(g.Imports, ImportLink{
			SourcePkg: pkgName,
			TargetPkg: path,
			Alias:     alias,
		})
	}
}

func extractFuncDecl(d *ast.FuncDecl, pkgName, fileName string, fset *token.FileSet) Symbol {
	sym := Symbol{
		Name:     d.Name.Name,
		Package:  pkgName,
		File:     fileName,
		Line:     fset.Position(d.Pos()).Line,
		Exported: d.Name.IsExported(),
	}
	if d.Doc != nil {
		sym.Doc = strings.TrimSpace(d.Doc.Text())
	}

	// Determine kind: method vs function
	if d.Recv != nil && len(d.Recv.List) > 0 {
		sym.Kind = "method"
		if se, ok := d.Recv.List[0].Type.(*ast.StarExpr); ok {
			if ident, ok := se.X.(*ast.Ident); ok {
				sym.Receiver = "*" + ident.Name
			}
		} else if ident, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
			sym.Receiver = ident.Name
		}
	} else {
		sym.Kind = "function"
	}

	// Extract calls within the function body
	sym.Calls = extractCalls(d.Body)
	return sym
}

func extractTypeSpec(s *ast.TypeSpec, pkgName, fileName string, fset *token.FileSet) Symbol {
	sym := Symbol{
		Name:     s.Name.Name,
		Package:  pkgName,
		File:     fileName,
		Line:     fset.Position(s.Pos()).Line,
		Exported: s.Name.IsExported(),
	}
	if s.Doc != nil {
		sym.Doc = strings.TrimSpace(s.Doc.Text())
	}

	switch t := s.Type.(type) {
	case *ast.StructType:
		sym.Kind = "struct"
		for _, field := range t.Fields.List {
			for _, name := range field.Names {
				typeName := "<unknown>"
				if ident, ok := field.Type.(*ast.Ident); ok {
					typeName = ident.Name
				} else if se, ok := field.Type.(*ast.StarExpr); ok {
					if ident, ok := se.X.(*ast.Ident); ok {
						typeName = "*" + ident.Name
					}
				}
				sym.Fields = append(sym.Fields, fmt.Sprintf("%s %s", name.Name, typeName))
			}
		}
	case *ast.InterfaceType:
		sym.Kind = "interface"
		for _, method := range t.Methods.List {
			if len(method.Names) > 0 {
				for _, name := range method.Names {
					sym.Methods = append(sym.Methods, name.Name)
				}
			}
		}
	default:
		sym.Kind = "type"
	}
	return sym
}

// extractCalls walks a function body and returns called function names.
func extractCalls(body *ast.BlockStmt) []string {
	if body == nil {
		return nil
	}
	var calls []string
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				calls = append(calls, fn.Name)
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok {
					calls = append(calls, x.Name+"."+fn.Sel.Name)
				}
			}
		}
		return true
	})
	return calls
}

// PrintSummary returns a human-readable summary of the graph.
func (g *Graph) PrintSummary() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Code Graph: %d files, %d symbols, %d packages\n\n", g.Files, len(g.Symbols), len(g.Packages)))

	// Count by kind
	kinds := make(map[string]int)
	for _, s := range g.Symbols {
		kinds[s.Kind]++
	}
	for k, v := range kinds {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", k, v))
	}
	return sb.String()
}
