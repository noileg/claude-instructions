package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const sample = `# Title
Preface

## Section A
A body 1
A body 2

### Section A-1
A-1 body

## Section B
B body
` + "```" + `
# not a heading inside a code block
` + "```" + `
B continued

## Section C
C body

### Section C-1
C-1 body

## Section A
same heading again

## 日本語の見出し
日本語の本文
`

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("doc.md", sample)
	must("crlf.md", "line 1\r\nline 2\r\n")
	must("bom.md", "\xef\xbb\xbfwith BOM\n")
	must("sjis.txt", "\x82\xa0\x82\xa2") // "あい" in CP932
	must("fence.md", "```go\nfmt.Println()\n```\n")
	must(".env", "SECRET=1\n")
	must("server.pem", "KEY")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProcess(t *testing.T) {
	dir := setup(t)
	cases := []struct {
		name    string
		delta   string
		want    string
		changed bool
	}{
		{"no marker leaves the text as is", "plain text\n", "plain text\n", false},
		{"whole file framed by divider lines", "before\n[[show: crlf.md]]\nafter\n",
			"before\n\n──── begin: `crlf.md` ────\n\nline 1\nline 2\n\n──── end ────\n\nafter\n", true},
		{"BOM is dropped", "[[show: bom.md]]\n", framed("bom.md", "with BOM\n"), true},
		{"line range", "[[show: doc.md:2-3]]\n", framed("doc.md:2-3", "Preface\n\n"), true},
		{"end of range is clamped", "[[show: crlf.md:2-99]]\n", framed("crlf.md:2-99", "line 2\n"), true},
		{"section stops at a higher heading", "[[show: doc.md#Section A-1]]\n",
			framed("doc.md#Section A-1", "### Section A-1\nA-1 body\n\n"), true},
		{"section includes lower headings", "[[show: doc.md#Section C]]\n",
			framed("doc.md#Section C", "## Section C\nC body\n\n### Section C-1\nC-1 body\n\n"), true},
		{"section stops at the same level, # in a code block is not a heading", "[[show: doc.md#Section B]]\n",
			framed("doc.md#Section B", "## Section B\nB body\n```\n# not a heading inside a code block\n```\nB continued\n\n"), true},
		{"heading may be written with its # marks", "[[show: doc.md##Section A-1]]\n",
			framed("doc.md##Section A-1", "### Section A-1\nA-1 body\n\n"), true},
		{"Japanese heading", "[[show: doc.md#日本語の見出し]]\n", framed("doc.md#日本語の見出し", "## 日本語の見出し\n日本語の本文\n"), true},
		{"raw wraps in a fence longer than the contents'", "[[show: fence.md | raw]]\n",
			framed("fence.md | raw", "````\n```go\nfmt.Println()\n```\n````\n"), true},
		{"囲む works like raw", "[[show: fence.md | 囲む]]\n", framed("fence.md | 囲む", "````\n```go\nfmt.Println()\n```\n````\n"), true},
		{"見せる marker works like show", "[[見せる: crlf.md:1-1]]\n", framed("crlf.md:1-1", "line 1\n"), true},
		{"leading space is not a marker", " [[show: crlf.md]]\n", " [[show: crlf.md]]\n", false},
		{"marker inside a line is not a marker", "see [[show: crlf.md]]\n", "see [[show: crlf.md]]\n", false},
		{"last batch without a line break", "[[show: crlf.md:1-1]]", framed("crlf.md:1-1", "line 1\n"), true},
		{"several markers", "[[show: crlf.md:1-1]]\nbetween\n[[show: crlf.md:2-2]]\n",
			framed("crlf.md:1-1", "line 1\n") + "between\n" + framed("crlf.md:2-2", "line 2\n"), true},
		{"file not found", "[[show: nothing.md]]\n", "[show failed: file not found: " + filepath.Join(dir, "nothing.md") + "]\n", true},
		{"directory", "[[show: sub]]\n", "[show failed: is a directory: " + filepath.Join(dir, "sub") + "]\n", true},
		{"heading not found", "[[show: doc.md#none]]\n", "[show failed: heading not found: \"none\"]\n", true},
		{"empty heading", "[[show: doc.md#]]\n", "[show failed: empty heading]\n", true},
		{"duplicate heading", "[[show: doc.md#Section A]]\n", "[show failed: 2 headings named \"Section A\"]\n", true},
		{"range out of bounds", "[[show: crlf.md:5-9]]\n", "[show failed: line range out of bounds: 5-9 (file has 2 lines)]\n", true},
		{"reversed range", "[[show: crlf.md:2-1]]\n", "[show failed: invalid line range: 2-1]\n", true},
		{"unknown option", "[[show: crlf.md | odd]]\n", "[show failed: unknown option \"odd\"]\n", true},
		{"not UTF-8", "[[show: sjis.txt]]\n", "[show failed: not valid UTF-8: " + filepath.Join(dir, "sjis.txt") + "]\n", true},
		{".env is refused", "[[show: .env]]\n", "[show failed: refused: may contain secrets]\n", true},
		{".pem is refused", "[[show: server.pem]]\n", "[show failed: refused: may contain secrets]\n", true},
		{"absolute path", "[[show: " + filepath.Join(dir, "crlf.md") + ":1-1]]\n", framed(filepath.Join(dir, "crlf.md")+":1-1", "line 1\n"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := process(c.delta, dir, catalog["en"])
			if changed != c.changed {
				t.Fatalf("changed=%v, want %v", changed, c.changed)
			}
			if got != c.want {
				t.Fatalf("wrong output\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// With -lang ja, divider lines and failure messages are Japanese, and both markers still work.
func TestProcessJapanese(t *testing.T) {
	dir := setup(t)
	cases := []struct{ name, delta, want string }{
		{"見せる marker", "[[見せる: crlf.md:1-1]]\n",
			"\n──── ここから：`crlf.md:1-1` ────\n\nline 1\n\n──── ここまで ────\n\n"},
		{"show marker", "[[show: crlf.md:1-1]]\n",
			"\n──── ここから：`crlf.md:1-1` ────\n\nline 1\n\n──── ここまで ────\n\n"},
		{"heading not found", "[[見せる: doc.md#none]]\n", "[見せる失敗: 見出しがない「none」]\n"},
		{"range out of bounds", "[[見せる: crlf.md:5-9]]\n", "[見せる失敗: 行範囲が範囲外: 5-9（全2行）]\n"},
		{".env is refused", "[[見せる: .env]]\n", "[見せる失敗: 読ませないファイル]\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := process(c.delta, dir, catalog["ja"])
			if got != c.want {
				t.Fatalf("wrong output\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// Every language must fill every message and take the same format verbs in the same
// order, because the same arguments are passed whatever the language is.
func TestCatalog(t *testing.T) {
	verbs := regexp.MustCompile(`%[sd]`)
	en := reflect.ValueOf(*catalog["en"])
	for lang, m := range catalog {
		v := reflect.ValueOf(*m)
		for i := 0; i < v.NumField(); i++ {
			name := v.Type().Field(i).Name
			s := v.Field(i).String()
			if s == "" {
				t.Errorf("%s.%s is empty", lang, name)
			}
			want := strings.Join(verbs.FindAllString(en.Field(i).String(), -1), "")
			if got := strings.Join(verbs.FindAllString(s, -1), ""); got != want {
				t.Errorf("%s.%s takes %q, en takes %q", lang, name, got, want)
			}
		}
	}
}

func TestParseLang(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "en"},
		{[]string{"-lang", "ja"}, "ja"},
		{[]string{"-lang=JA"}, "ja"},
		{[]string{"-lang", "fr"}, "en"},
		{[]string{"-unknown"}, "en"},
	}
	for _, c := range cases {
		if got := parseLang(c.args); got != c.want {
			t.Errorf("parseLang(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

// From the JSON on stdin to the JSON on stdout.
func TestRun(t *testing.T) {
	dir := setup(t)

	// No marker: print nothing, so the original text is shown.
	var out bytes.Buffer
	in := `{"cwd":` + jsonStr(dir) + `,"delta":"plain text\n","final":false}`
	if err := run(nil, strings.NewReader(in), &out); err != nil || out.Len() != 0 {
		t.Fatalf("output without a marker: err=%v out=%q", err, out.String())
	}

	// A marker: return displayContent as JSON, without escaping <>&.
	in = `{"cwd":` + jsonStr(dir) + `,"delta":"[[show: crlf.md:1-1]]\n"}`
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, framed("crlf.md:1-1", "line 1\n")},
		{[]string{"-lang", "ja"}, "\n──── ここから：`crlf.md:1-1` ────\n\nline 1\n\n──── ここまで ────\n\n"},
	} {
		out.Reset()
		if err := run(c.args, strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		var o output
		if err := json.Unmarshal(out.Bytes(), &o); err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, out.String())
		}
		if o.HookSpecificOutput.HookEventName != "MessageDisplay" || o.HookSpecificOutput.DisplayContent != c.want {
			t.Fatalf("wrong output for %q: %+v", c.args, o)
		}
	}

	// Broken input: return an error and print nothing.
	out.Reset()
	if err := run(nil, strings.NewReader("not json"), &out); err == nil || out.Len() != 0 {
		t.Fatalf("broken input: err=%v out=%q", err, out.String())
	}
}

// framed builds the expected English output. The format itself is checked literally
// in "whole file framed by divider lines".
func framed(spec, body string) string {
	return "\n──── begin: `" + spec + "` ────\n\n" + body + "\n──── end ────\n\n"
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
