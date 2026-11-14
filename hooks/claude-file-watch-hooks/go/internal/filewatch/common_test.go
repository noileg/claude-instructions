package filewatch

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
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

func useTempState(t *testing.T) {
	t.Helper()
	t.Setenv("FILE_WATCH_STATE_DIR", t.TempDir())
}

func TestWriteEntryAndLoadSession(t *testing.T) {
	useTempState(t)
	dir := SessionDir("s1")
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []Snapshot{
		TextSnapshot(`C:\b\二.md`, "two\n"),
		TextSnapshot(`C:\a\one.md`, "one\n"),
		HashOnlySnapshot(`C:\c\three.png`, "abc", info),
	}
	for _, s := range want {
		if err := WriteEntry(dir, s.Path, s); err != nil {
			t.Fatal(err)
		}
	}
	// Replacing keeps one state file per path.
	want[1] = TextSnapshot(`C:\a\one.md`, "one, edited\n")
	if err := WriteEntry(dir, want[1].Path, want[1]); err != nil {
		t.Fatal(err)
	}
	// Leftover temporary files and broken state files are skipped.
	os.WriteFile(filepath.Join(dir, "x"+tmpExt), []byte(`{"path":"C:\\tmp.md"}`), 0644)
	os.WriteFile(filepath.Join(dir, "broken"+entryExt), []byte(`{`), 0644)

	got := LoadSession("s1")
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if LoadSession("other") != nil {
		t.Error("a session without state should have no snapshots")
	}
}

func TestWriteEntryParallel(t *testing.T) {
	useTempState(t)
	dir := SessionDir("s1")
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmt.Sprintf(`C:\f%d.md`, i)
			if err := WriteEntry(dir, p, TextSnapshot(p, "x")); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(LoadSession("s1")); got != n {
		t.Errorf("got %d snapshots, want %d", got, n)
	}
}

func TestLoadEdit(t *testing.T) {
	useTempState(t)
	e := EditEntry{Path: `C:\a.md`, Hash: "h", SessionID: "s1"}
	if err := WriteEntry(EditsDir(), e.Path, e); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadEdit(e.Path); !ok || got != e {
		t.Errorf("got %+v, %v", got, ok)
	}
	if _, ok := LoadEdit(`C:\b.md`); ok {
		t.Error("a file without a record should have none")
	}
	// A record whose path does not match (a hash collision, or a damaged file) is ignored.
	other := EditEntry{Path: `C:\c.md`, Hash: "h", SessionID: "s1"}
	if err := WriteEntry(EditsDir(), e.Path, other); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadEdit(e.Path); ok {
		t.Error("a record for another path should be ignored")
	}
}

// While another process has the state file open, the rename fails on Windows; WriteEntry
// tries again until it is closed, and gives up (keeping the old state file) if it is not.
func TestWriteEntryWhileOpen(t *testing.T) {
	useTempState(t)
	oldAttempts, oldWait := renameAttempts, renameWait
	renameAttempts, renameWait = 5, 20*time.Millisecond
	defer func() { renameAttempts, renameWait = oldAttempts, oldWait }()

	dir := SessionDir("s1")
	p := `C:\a.md`
	if err := WriteEntry(dir, p, TextSnapshot(p, "v1")); err != nil {
		t.Fatal(err)
	}
	entry := EntryPath(dir, p)

	f, err := os.Open(entry)
	if err != nil {
		t.Fatal(err)
	}
	go func(f *os.File) {
		time.Sleep(30 * time.Millisecond)
		f.Close()
	}(f)
	if err := WriteEntry(dir, p, TextSnapshot(p, "v2")); err != nil {
		t.Errorf("should succeed once the file is closed: %v", err)
	}
	if s := LoadSession("s1"); len(s) != 1 || s[0].Content != "v2" {
		t.Errorf("got %+v", s)
	}

	f, err = os.Open(entry)
	if err != nil {
		t.Fatal(err)
	}
	err = WriteEntry(dir, p, TextSnapshot(p, "v3"))
	f.Close()
	if err == nil {
		t.Error("should fail while the file stays open")
	}
	if s := LoadSession("s1"); len(s) != 1 || s[0].Content != "v2" {
		t.Errorf("the old state file should stay, got %+v", s)
	}
	names, _ := os.ReadDir(dir)
	for _, n := range names {
		if filepath.Ext(n.Name()) == tmpExt {
			t.Errorf("temporary file left behind: %s", n.Name())
		}
	}
}
