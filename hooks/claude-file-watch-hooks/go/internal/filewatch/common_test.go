package filewatch

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func keys(s ExtensionSet) []string {
	var out []string
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestLoadExtensionConfigNormalises(t *testing.T) {
	p := writeConfig(t, "設定 フォルダ/拡張子 一覧.json", `{"extensions": [".MD", "Foo", " .Bar ", "", ".md"]}`)
	set, cerr := LoadExtensionConfig(p)
	if cerr != nil {
		t.Fatal(cerr)
	}
	if got, want := keys(set), []string{".bar", ".foo", ".md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLoadExtensionConfigBOMAndEmpty(t *testing.T) {
	set, cerr := LoadExtensionConfig(writeConfig(t, "a.json", "\xEF\xBB\xBF{\"extensions\": []}"))
	if cerr != nil || len(set) != 0 {
		t.Errorf("BOM + empty array should load as an empty set, got %v, %v", set, cerr)
	}
}

func TestLoadExtensionConfigErrors(t *testing.T) {
	cases := map[string]string{
		ConfigBadJSON:      `{"extensions": [".md"`,
		ConfigNoExtensions: `{"exts": [".md"]}`,
	}
	for reason, content := range cases {
		if _, cerr := LoadExtensionConfig(writeConfig(t, "a.json", content)); cerr == nil || cerr.Reason != reason {
			t.Errorf("%s: got %v", content, cerr)
		}
	}
	for _, content := range []string{`{"extensions": ".md"}`, `{"extensions": null}`, `{"extensions": [1]}`, `{"extensions": {"a": 1}}`, `null`} {
		if _, cerr := LoadExtensionConfig(writeConfig(t, "a.json", content)); cerr == nil || cerr.Reason != ConfigNoExtensions {
			t.Errorf("%s: got %v", content, cerr)
		}
	}
	if _, cerr := LoadExtensionConfig(writeConfig(t, "a.json", `[".md"]`)); cerr == nil || cerr.Reason != ConfigBadJSON {
		t.Errorf("top-level array: got %v", cerr)
	}
	if _, cerr := LoadExtensionConfig(filepath.Join(t.TempDir(), "missing.json")); cerr == nil || cerr.Reason != ConfigNotFound {
		t.Errorf("missing file: got %v", cerr)
	}
	if _, cerr := LoadExtensionConfig(t.TempDir()); cerr == nil || cerr.Reason != ConfigUnreadable {
		t.Errorf("directory: got %v", cerr)
	}
}

func TestExtensionsFromArgs(t *testing.T) {
	md := writeConfig(t, "md.json", `{"extensions": ["MD"]}`)
	foo := writeConfig(t, "foo.json", `{"extensions": ["foo"]}`)
	bad := writeConfig(t, "bad.json", `{`)

	set, cerr := ExtensionsFromArgs([]string{"x"})
	if cerr != nil || len(set) != len(TextExtensions) {
		t.Errorf("no flags: %d entries, %v", len(set), cerr)
	}

	set, cerr = ExtensionsFromArgs([]string{"x", "-lang", "ja", "-config", md})
	if cerr != nil || !reflect.DeepEqual(keys(set), []string{".md"}) {
		t.Errorf("-config: %v, %v", keys(set), cerr)
	}

	set, cerr = ExtensionsFromArgs([]string{"x", "-config-add", foo})
	if cerr != nil || !set.Has("a.foo") || !set.Has("a.txt") || len(set) != len(TextExtensions)+1 {
		t.Errorf("-config-add: %d entries, %v", len(set), cerr)
	}
	if TextExtensions.Has("a.foo") {
		t.Error("-config-add modified the built-in list")
	}

	// A file that cannot be used, or both flags: back to the built-in list, with the reason.
	for _, args := range [][]string{
		{"x", "-config", bad},
		{"x", "-config-add", bad},
		{"x", "-config", md, "-config-add", foo},
	} {
		set, cerr = ExtensionsFromArgs(args)
		if cerr == nil || len(set) != len(TextExtensions) || !set.Has("a.txt") {
			t.Errorf("%q: %d entries, %v", args, len(set), cerr)
		}
	}
	if _, cerr = ExtensionsFromArgs([]string{"x", "-config", md, "-config-add", foo}); cerr.Reason != ConfigBothGiven {
		t.Errorf("both: reason %s", cerr.Reason)
	}

	// An empty value counts as not given.
	if _, cerr = ExtensionsFromArgs([]string{"x", "-config", "", "-config-add", foo}); cerr != nil {
		t.Errorf("empty -config with -config-add: %v", cerr)
	}
	// -config with an empty array: nothing is tracked as text.
	empty := writeConfig(t, "empty.json", `{"extensions": []}`)
	if set, _ = ExtensionsFromArgs([]string{"x", "-config", empty}); len(set) != 0 || set.Has("a.txt") {
		t.Errorf("-config with empty array: %v", keys(set))
	}
	if set, _ = ExtensionsFromArgs([]string{"x", "-config-add", empty}); len(set) != len(TextExtensions) {
		t.Errorf("-config-add with empty array: %d entries", len(set))
	}
}
