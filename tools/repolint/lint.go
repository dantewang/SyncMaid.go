package repolint

import (
	"cmp"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Violation describes a static check failure reported by Check.
type Violation struct {
	File    string
	Line    int
	Col     int
	Rule    string
	Message string
}

func (v Violation) String() string {
	if v.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: [%s] %s", v.File, v.Line, v.Col, v.Rule, v.Message)
	}
	return fmt.Sprintf("%s: [%s] %s", v.File, v.Rule, v.Message)
}

type parsedFile struct {
	rel  string
	file *ast.File
}

var skipDirs = map[string]bool{
	"assets":  true,
	"build":   true,
	"Data":    true,
	"docs":    true,
	"e2e":     true,
	"locales": true,
	"scripts": true,
	"tools":   true,
}

var rawOSIOFuncs = map[string]bool{
	"Stat":       true,
	"ReadFile":   true,
	"WriteFile":  true,
	"Rename":     true,
	"CreateTemp": true,
	"RemoveAll":  true,
}

var interpolationVerbs = regexp.MustCompile(`%[-+.0-9]*[sdf]`)

// Check runs all repository lint rules under repoRoot using only the Go standard library.
func Check(repoRoot string) ([]Violation, error) {
	root := filepath.Clean(repoRoot)
	fset := token.NewFileSet()
	var parsed []parsedFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (skipDirs[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.ToSlash(rel), err)
		}
		parsed = append(parsed, parsedFile{rel: filepath.ToSlash(rel), file: f})
		return nil
	})
	if err != nil {
		return nil, err
	}

	var violations []Violation
	for _, pf := range parsed {
		violations = append(violations, checkArchImports(fset, pf.rel, pf.file)...)
		violations = append(violations, checkNoRawOSIO(fset, pf.rel, pf.file)...)
	}
	i18nViolations, err := checkI18nCatalog(root, fset, parsed)
	if err != nil {
		return nil, err
	}
	violations = append(violations, i18nViolations...)

	slices.SortFunc(violations, func(a, b Violation) int {
		return cmp.Or(
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Col, b.Col),
			cmp.Compare(a.Rule, b.Rule),
			cmp.Compare(a.Message, b.Message),
		)
	})
	return violations, nil
}

func checkArchImports(fset *token.FileSet, rel string, f *ast.File) []Violation {
	var out []Violation
	isInternal := strings.HasPrefix(rel, "internal/")
	isPureCore := strings.HasPrefix(rel, "internal/taskconfig/") || strings.HasPrefix(rel, "internal/filefilter/")
	e2eOnly := requiresE2E(f)

	for _, imp := range f.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		pos := fset.Position(imp.Pos())
		add := func(msg string) {
			out = append(out, Violation{File: rel, Line: pos.Line, Col: pos.Column, Rule: "arch-imports", Message: msg})
		}

		if isInternal && importsPrefix(pkg, "github.com/egoist/mygo") {
			add(fmt.Sprintf("internal package must not import %q", pkg))
		}
		if isPureCore && (importsPrefix(pkg, "os") ||
			importsPrefix(pkg, "golang.org/x/sys/windows") ||
			importsPrefix(pkg, "syncmaid/internal/windowsfs") ||
			importsPrefix(pkg, "syncmaid/internal/winutil")) {
			add(fmt.Sprintf("model/filter package must stay free of OS/filesystem dependency %q", pkg))
		}
		if pkg == "os/exec" || strings.Contains(strings.ToLower(pkg), "webview") {
			add(fmt.Sprintf("production code must not import %q", pkg))
		}
		if importsPrefix(pkg, "github.com/egoist/mygo/plugins/automation") && !e2eOnly {
			add(fmt.Sprintf("%q may only be imported in //go:build e2e files", pkg))
		}
	}
	return out
}

func importsPrefix(pkg, target string) bool {
	return pkg == target || strings.HasPrefix(pkg, target+"/")
}

// requiresE2E reports whether build directives require tag "e2e" (i.e. setting e2e=false disables the file).
func requiresE2E(f *ast.File) bool {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				continue
			}
			if !expr.Eval(func(tag string) bool { return tag != "e2e" }) {
				return true
			}
		}
	}
	return false
}

func checkNoRawOSIO(fset *token.FileSet, rel string, f *ast.File) []Violation {
	osAliases := map[string]bool{}
	for _, imp := range f.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil || pkg != "os" {
			continue
		}
		if imp.Name == nil {
			osAliases["os"] = true
		} else if imp.Name.Name != "_" && imp.Name.Name != "." {
			osAliases[imp.Name.Name] = true
		}
	}
	if len(osAliases) == 0 {
		return nil
	}

	var out []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !rawOSIOFuncs[sel.Sel.Name] {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || !osAliases[id.Name] {
			return true
		}
		if sel.Sel.Name == "CreateTemp" && (rel == "internal/atomicfile/atomic.go" || rel == "internal/taskstore/io.go") {
			return true
		}
		pos := fset.Position(call.Pos())
		out = append(out, Violation{
			File:    rel,
			Line:    pos.Line,
			Col:     pos.Column,
			Rule:    "no-raw-os-io",
			Message: fmt.Sprintf("avoid raw os.%s; use internal/atomicfile or internal/windowsfs", sel.Sel.Name),
		})
		return true
	})
	return out
}

func checkI18nCatalog(root string, fset *token.FileSet, files []parsedFile) ([]Violation, error) {
	localesDir := filepath.Join(root, "locales")
	entries, err := os.ReadDir(localesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Violation{{
				File:    "locales/en.json",
				Rule:    "i18n-catalog",
				Message: "missing required base locale catalog locales/en.json",
			}}, nil
		}
		return nil, err
	}

	var jsonNames []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonNames = append(jsonNames, e.Name())
		}
	}
	slices.Sort(jsonNames)

	var out []Violation
	tables := map[string]map[string]string{}
	for _, name := range jsonNames {
		relLocale := "locales/" + name
		data, err := os.ReadFile(filepath.Join(localesDir, name))
		if err != nil {
			return nil, err
		}
		var table map[string]string
		if err := json.Unmarshal(data, &table); err != nil {
			out = append(out, Violation{File: relLocale, Rule: "i18n-catalog", Message: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		tables[name] = table

		keys := sortedKeys(table)
		for _, k := range keys {
			v := table[k]
			if v == "" || !slices.Equal(interpolationVerbs.FindAllString(k, -1), interpolationVerbs.FindAllString(v, -1)) {
				out = append(out, Violation{
					File:    relLocale,
					Rule:    "i18n-catalog",
					Message: fmt.Sprintf("invalid or empty translation/interpolation for %q", k),
				})
			}
		}
	}

	enTable, hasEn := tables["en.json"]
	if !hasEn {
		if !slices.Contains(jsonNames, "en.json") {
			out = append(out, Violation{
				File:    "locales/en.json",
				Rule:    "i18n-catalog",
				Message: "missing required base locale catalog locales/en.json",
			})
		}
		return out, nil
	}

	enKeys := sortedKeys(enTable)
	for _, name := range jsonNames {
		if name == "en.json" {
			continue
		}
		table, ok := tables[name]
		if !ok {
			continue
		}
		relLocale := "locales/" + name
		for _, k := range enKeys {
			if _, exists := table[k]; !exists {
				out = append(out, Violation{
					File:    relLocale,
					Rule:    "i18n-catalog",
					Message: fmt.Sprintf("missing translation key %q", k),
				})
			}
		}
		for _, k := range sortedKeys(table) {
			if _, exists := enTable[k]; !exists {
				out = append(out, Violation{
					File:    relLocale,
					Rule:    "i18n-catalog",
					Message: fmt.Sprintf("extra translation key %q not in locales/en.json", k),
				})
			}
		}
	}

	for _, pf := range files {
		ast.Inspect(pf.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			isMessage := false
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				isMessage = fun.Name == "messagef"
			case *ast.SelectorExpr:
				isMessage = fun.Sel.Name == "tr"
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !isMessage || !ok || lit.Kind != token.STRING {
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if strings.IndexFunc(key, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
				if _, exists := enTable[key]; !exists {
					pos := fset.Position(lit.Pos())
					out = append(out, Violation{
						File:    pf.rel,
						Line:    pos.Line,
						Col:     pos.Column,
						Rule:    "i18n-catalog",
						Message: fmt.Sprintf("missing translation key %q in locales/en.json", key),
					})
				}
			}
			return true
		})
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
