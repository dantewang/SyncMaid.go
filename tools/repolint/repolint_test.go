package repolint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func hasRule(vs []Violation, fileSub, rule, msgSub string) bool {
	return slices.ContainsFunc(vs, func(v Violation) bool {
		return strings.Contains(v.File, fileSub) && v.Rule == rule && strings.Contains(v.Message, msgSub)
	})
}

func TestArchImportsRule(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"locales/en.json": `{}`,
		"internal/bad/bad.go": `package bad
import _ "github.com/egoist/mygo/ui"
`,
		"internal/taskconfig/model.go": `package taskconfig
import (
	_ "os"
	_ "golang.org/x/sys/windows"
	_ "syncmaid/internal/windowsfs"
	_ "syncmaid/internal/winutil"
)
`,
		"internal/filefilter/filter.go": `package filefilter
import _ "os"
`,
		"cmd_bad.go": `package main
import (
	_ "os/exec"
	_ "example.com/webview2"
	_ "github.com/egoist/mygo/plugins/automation"
)
`,
		"e2e_or_windows.go": `//go:build e2e || windows
package main
import _ "github.com/egoist/mygo/plugins/automation"
`,
		"e2e_ok.go": `//go:build e2e
package main
import _ "github.com/egoist/mygo/plugins/automation"
`,
		"e2e_and_windows_ok.go": `//go:build e2e && windows
package main
import _ "github.com/egoist/mygo/plugins/automation"
`,
		"e2e/should_skip.go": `package e2e
import _ "os/exec"
`,
		"root_test.go": `package main
import _ "os/exec"
`,
		"tools/helper/main.go": `package main
import _ "os/exec"
`,
	})

	vs, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	wantChecks := []struct {
		file string
		msg  string
	}{
		{"internal/bad/bad.go", "github.com/egoist/mygo/ui"},
		{"internal/taskconfig/model.go", `"os"`},
		{"internal/taskconfig/model.go", "golang.org/x/sys/windows"},
		{"internal/taskconfig/model.go", "syncmaid/internal/windowsfs"},
		{"internal/taskconfig/model.go", "syncmaid/internal/winutil"},
		{"internal/filefilter/filter.go", `"os"`},
		{"cmd_bad.go", `"os/exec"`},
		{"cmd_bad.go", "webview2"},
		{"cmd_bad.go", "plugins/automation"},
		{"e2e_or_windows.go", "plugins/automation"},
	}
	for _, w := range wantChecks {
		if !hasRule(vs, w.file, "arch-imports", w.msg) {
			t.Errorf("expected arch-imports violation for (%s, %s); got:\n%v", w.file, w.msg, vs)
		}
	}
	for _, forbiddenFile := range []string{"e2e_ok.go", "e2e_and_windows_ok.go", "e2e/should_skip.go", "root_test.go", "tools/helper/main.go"} {
		if slices.ContainsFunc(vs, func(v Violation) bool { return strings.Contains(v.File, forbiddenFile) }) {
			t.Errorf("unexpected violation in skipped/allowed file %s: %v", forbiddenFile, vs)
		}
	}
}

func TestNoRawOSIORule(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"locales/en.json": `{}`,
		"caller.go": `package main
import myos "os"
func bad() {
	_, _ = myos.Stat("a")
	_, _ = myos.ReadFile("b")
	_ = myos.WriteFile("c", nil, 0600)
	_ = myos.Rename("d", "e")
	_, _ = myos.CreateTemp(".", "tmp")
	_ = myos.RemoveAll("f")
	_, _ = myos.Lstat("g")
	_, _ = myos.Open("h")
}
`,
		"internal/atomicfile/atomic.go": `package atomicfile
import "os"
func ok() {
	_, _ = os.CreateTemp(".", "p")
	_ = os.WriteFile("bad", nil, 0600)
}
`,
		"internal/taskstore/io.go": `package taskstore
import "os"
func ok() {
	_, _ = os.CreateTemp(".", "p")
}
`,
	})

	vs, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"os.Stat", "os.ReadFile", "os.WriteFile", "os.Rename", "os.CreateTemp", "os.RemoveAll"} {
		if !hasRule(vs, "caller.go", "no-raw-os-io", fn) {
			t.Errorf("expected no-raw-os-io for %s in caller.go; got %v", fn, vs)
		}
	}
	for _, allowed := range []string{"os.Lstat", "os.Open"} {
		if hasRule(vs, "caller.go", "no-raw-os-io", allowed) {
			t.Errorf("unexpected no-raw-os-io for allowed %s", allowed)
		}
	}
	if !hasRule(vs, "internal/atomicfile/atomic.go", "no-raw-os-io", "os.WriteFile") {
		t.Errorf("expected os.WriteFile to be flagged in internal/atomicfile/atomic.go; got %v", vs)
	}
	if hasRule(vs, "internal/atomicfile/atomic.go", "no-raw-os-io", "os.CreateTemp") {
		t.Errorf("unexpected flag on whitelisted CreateTemp in internal/atomicfile/atomic.go: %v", vs)
	}
	if hasRule(vs, "internal/taskstore/io.go", "no-raw-os-io", "") {
		t.Errorf("unexpected flag in internal/taskstore/io.go: %v", vs)
	}
}

func TestI18nCatalogRule(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"locales/en.json": `{"已有 %s": "Has %s", "缺失": "Missing"}`,
		"locales/ja.json": `{"已有 %s": "Bad %d", "多余": "Extra", "空值": ""}`,
		"ui.go": `package main
type app struct{}
func (app) tr(k string, a ...any) string { return k }
func messagef(k string, a ...any) string { return k }
func demo(a app) {
	_ = a.tr("已有 %s", 1)
	_ = a.tr("未翻译")
	_ = messagef("另一个缺失 %d", 2)
}
`,
	})

	vs, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		file string
		msg  string
	}{
		{"locales/ja.json", `invalid or empty translation/interpolation for "已有 %s"`},
		{"locales/ja.json", `invalid or empty translation/interpolation for "空值"`},
		{"locales/ja.json", `missing translation key "缺失"`},
		{"locales/ja.json", `extra translation key "多余"`},
		{"ui.go", `missing translation key "未翻译"`},
		{"ui.go", `missing translation key "另一个缺失 %d"`},
	}
	for _, c := range checks {
		if !hasRule(vs, c.file, "i18n-catalog", c.msg) {
			t.Errorf("expected i18n-catalog (%s, %s); got %v", c.file, c.msg, vs)
		}
	}
	for _, v := range vs {
		if v.File == "ui.go" && (v.Line <= 0 || v.Col <= 0) {
			t.Errorf("expected exact line/col for ui.go violation, got %+v", v)
		}
	}
}
