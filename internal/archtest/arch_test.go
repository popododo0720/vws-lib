// Package archtest enforces this module's layer boundaries on the imports of
// every non-test Go file. Modeled on vws-compute internal/archtest.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/popododo0720/vws-lib"

func isModule(imp string) bool { return imp == module || strings.HasPrefix(imp, module+"/") }

// listed reports whether dir is one of the named packages, not a package below one.
func listed(dir string, dirs ...string) bool {
	for _, d := range dirs {
		if dir == d {
			return true
		}
	}
	return false
}

func hasPrefix(prefixes ...string) func(string) bool {
	return func(s string) bool {
		for _, p := range prefixes {
			if s == p || strings.HasPrefix(s, p+"/") {
				return true
			}
		}
		return false
	}
}

var (
	isSentry  = hasPrefix("github.com/getsentry/sentry-go")
	isOTel    = hasPrefix("go.opentelemetry.io/otel")
	isOTelSDK = hasPrefix("go.opentelemetry.io/otel/sdk", "go.opentelemetry.io/otel/exporters")
)

// packageRules forbid imports inside one package directory.
var packageRules = []struct {
	dir    string
	forbid func(imp string) bool
	why    string
}{
	{"errs", func(imp string) bool {
		return isModule(imp) || isSentry(imp) || isOTel(imp) || imp == "log" || imp == "log/slog"
	}, "errs is the domain: no module, telemetry or logging package"},
}

// ownerRules confine an import to the packages that own it.
var ownerRules = []struct {
	match  func(imp string) bool
	owners []string
	why    string
}{
	{isSentry, []string{"obs/report"}, "sentry-go stays in obs/report"},
	{isOTelSDK, []string{"obs"}, "the OpenTelemetry SDK and exporters stay in obs; adapters use the API"},
	{hasPrefix("k8s.io", "sigs.k8s.io"), nil, "this library carries no Kubernetes client"},
	{func(imp string) bool { return imp == "log" }, nil, "no std log: use log/slog"},
	{func(imp string) bool { return imp == "testing" }, nil, "production code does not import testing"},
}

// constrainedDirs are the directories a rule names. Renaming one would
// silently disable its rule, so each must exist.
func constrainedDirs() []string {
	var dirs []string
	for _, r := range packageRules {
		dirs = append(dirs, r.dir)
	}
	for _, r := range ownerRules {
		dirs = append(dirs, r.owners...)
	}
	return dirs
}

// archViolations applies every rule to the non-test files in pkgs, keyed by
// slash-separated directory relative to the module root.
func archViolations(pkgs map[string][]*ast.File) []string {
	var bad []string
	for dir, files := range pkgs {
		for _, f := range files {
			for _, spec := range f.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					bad = append(bad, fmt.Sprintf("%s: import path %s: %v", dir, spec.Path.Value, err))
					continue
				}
				for _, r := range packageRules {
					if dir == r.dir && r.forbid(imp) {
						bad = append(bad, fmt.Sprintf("%s imports %s: %s", dir, imp, r.why))
					}
				}
				for _, r := range ownerRules {
					if r.match(imp) && !listed(dir, r.owners...) {
						bad = append(bad, fmt.Sprintf("%s imports %s: %s", dir, imp, r.why))
					}
				}
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func walkFiles(root string, visit func(rel string, data []byte)) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .worktrees holds local git worktrees, not module source, when the test runs in the main checkout.
			switch d.Name() {
			case ".git", ".worktrees", "third_party", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", path, err)
		}
		visit(filepath.ToSlash(rel), data)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}
	return nil
}

func loadProduction(t *testing.T, root string) map[string][]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	pkgs := map[string][]*ast.File{}
	err := walkFiles(root, func(rel string, data []byte) {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return
		}
		f, err := parser.ParseFile(fset, rel, data, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		pkgs[dir] = append(pkgs[dir], f)
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureRules(t *testing.T) {
	probe := t.TempDir()
	for rel, src := range map[string]string{
		"errs/e.go":        `package errs; import _ "log/slog"; import _ "` + module + `/obs"`,
		"obs/o.go":         `package obs; import _ "github.com/getsentry/sentry-go"`,
		"obs/l.go":         `package obs; import _ "log"`,
		"obs/grpcobs/g.go": `package grpcobs; import _ "go.opentelemetry.io/otel/sdk/trace"`,
		"obs/k8s.go":       `package obs; import _ "k8s.io/apimachinery/pkg/types"`,
		"obs/report/r.go":  `package report; import _ "github.com/getsentry/sentry-go"`,
		"obs/health/h.go":  `package health; import _ "testing"; var _ testing.TB`,
		"obs/obs_test.go":  `package obs; import _ "log"`,
	} {
		writeFile(t, filepath.Join(probe, rel), src)
	}
	want := []string{
		"errs imports " + module + "/obs: errs is the domain: no module, telemetry or logging package",
		"errs imports log/slog: errs is the domain: no module, telemetry or logging package",
		"obs imports github.com/getsentry/sentry-go: sentry-go stays in obs/report",
		"obs imports log: no std log: use log/slog",
		"obs imports k8s.io/apimachinery/pkg/types: this library carries no Kubernetes client",
		"obs/grpcobs imports go.opentelemetry.io/otel/sdk/trace: " +
			"the OpenTelemetry SDK and exporters stay in obs; adapters use the API",
		"obs/health imports testing: production code does not import testing",
	}
	sort.Strings(want)
	if got := archViolations(loadProduction(t, probe)); !reflect.DeepEqual(got, want) {
		t.Fatalf("probe violations:\n got %q\nwant %q", got, want)
	}

	root := repoRoot(t)
	for _, dir := range constrainedDirs() {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Errorf("rule directory %s: %v", dir, err)
		}
	}
	for _, v := range archViolations(loadProduction(t, root)) {
		t.Error(v)
	}
}
