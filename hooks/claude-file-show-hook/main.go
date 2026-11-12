// Command claude-file-show-hook is a Claude Code MessageDisplay hook. It replaces a
// marker line in Claude's reply with the contents of a file, on screen only.
//
// Why: when Claude retypes a file to show it, the output tokens are wasted; when it
// prints the file with a tool, the contents go into its context. MessageDisplay is
// display-only: the replacement reaches neither Claude's context nor the transcript.
// Claude writes only the one marker line, and this hook copies the contents from the
// file mechanically.
//
// Markers (valid only when the line starts with the marker and holds nothing else;
// "show" and "見せる" are both accepted whatever -lang is):
//
//	[[show: path]]            whole file
//	[[show: path#heading]]    section: the heading line up to the next heading of the same or a higher level
//	[[show: path:10-40]]      line range (1-based, inclusive)
//	[[show: ... | raw]]       wrap in a code block to show the text verbatim ("| 囲む" also works)
//
// The contents are framed by divider lines so they can be told apart from the reply.
// Divider lines and failure messages follow -lang (en, the default, or ja).
// A batch without a marker produces no output, so the original text is shown.
// No state is kept between calls: a marker fits in one line, so it never spans batches.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Files larger than this are reported as a failure instead of being shown.
const maxFileBytes = 5 << 20

// messages is the text this hook puts on screen, in one language.
type messages struct {
	begin, end   string // divider lines; begin takes the marker spec
	failed       string // takes the reason of a failure
	unknownOpt   string
	emptyPath    string
	emptyHeading string
	denied       string
	notFound     string
	isDir        string
	tooLarge     string
	notUTF8      string
	badRange     string
	outOfRange   string
	noHeading    string
	dupHeading   string
}

var catalog = map[string]*messages{
	"en": {
		begin:        "──── begin: `%s` ────",
		end:          "──── end ────",
		failed:       "[show failed: %s]",
		unknownOpt:   `unknown option "%s"`,
		emptyPath:    "empty path",
		emptyHeading: "empty heading",
		denied:       "refused: may contain secrets",
		notFound:     "file not found: %s",
		isDir:        "is a directory: %s",
		tooLarge:     "too large (%d bytes, limit %d)",
		notUTF8:      "not valid UTF-8: %s",
		badRange:     "invalid line range: %d-%d",
		outOfRange:   "line range out of bounds: %d-%d (file has %d lines)",
		noHeading:    `heading not found: "%s"`,
		dupHeading:   `%d headings named "%s"`,
	},
	"ja": {
		begin:        "──── ここから：`%s` ────",
		end:          "──── ここまで ────",
		failed:       "[見せる失敗: %s]",
		unknownOpt:   "不明なオプション「%s」",
		emptyPath:    "パスが空",
		emptyHeading: "見出しが空",
		denied:       "読ませないファイル",
		notFound:     "ファイルがない: %s",
		isDir:        "ディレクトリは見せられない: %s",
		tooLarge:     "大きすぎる（%d バイト、上限 %d）",
		notUTF8:      "UTF-8として読めない: %s",
		badRange:     "行範囲が不正: %d-%d",
		outOfRange:   "行範囲が範囲外: %d-%d（全%d行）",
		noHeading:    "見出しがない「%s」",
		dupHeading:   "同名の見出しが%d個ある「%s」",
	},
}

type input struct {
	Cwd   string `json:"cwd"`
	Delta string `json:"delta"`
}

type output struct {
	HookSpecificOutput struct {
		HookEventName  string `json:"hookEventName"`
		DisplayContent string `json:"displayContent"`
	} `json:"hookSpecificOutput"`
}

var (
	markerRe  = regexp.MustCompile(`^\[\[(?:show|見せる):[ \t]*(.+?)[ \t]*\]\]$`)
	rangeRe   = regexp.MustCompile(`:(\d+)-(\d+)$`)
	headingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	fenceRe   = regexp.MustCompile("^[ \t]{0,3}(```|~~~)")
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		// Print nothing on failure; Claude Code then shows the original text.
		os.Exit(0)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	m := catalog[parseLang(args)]
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	out, changed := process(in.Delta, in.Cwd, m)
	if !changed {
		return nil
	}
	var o output
	o.HookSpecificOutput.HookEventName = "MessageDisplay"
	o.HookSpecificOutput.DisplayContent = out
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}

// parseLang returns the -lang value. Anything it cannot use falls back to English,
// because a mistake in the registered command must not stop files from being shown.
func parseLang(args []string) string {
	fs := flag.NewFlagSet("claude-file-show-hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lang := fs.String("lang", "en", "language of divider lines and failure messages (en or ja)")
	if fs.Parse(args) != nil {
		return "en"
	}
	l := strings.ToLower(*lang)
	if _, ok := catalog[l]; !ok {
		return "en"
	}
	return l
}

// process replaces the marker lines in delta and leaves every other line as is.
// changed is false when there was nothing to replace.
func process(delta, cwd string, m *messages) (out string, changed bool) {
	if !strings.Contains(delta, "[[show:") && !strings.Contains(delta, "[[見せる:") {
		return delta, false
	}
	var sb strings.Builder
	for _, line := range strings.SplitAfter(delta, "\n") {
		body := strings.TrimRight(line, "\r\n")
		mk := markerRe.FindStringSubmatch(body)
		if mk == nil {
			sb.WriteString(line)
			continue
		}
		changed = true
		content, err := render(mk[1], cwd, m)
		if err != nil {
			content = fmt.Sprintf(m.failed, err.Error())
		} else {
			content = frame(mk[1], content, m)
		}
		sb.WriteString(content)
		// Keep the line break that ended the marker line.
		if strings.HasSuffix(line, "\n") && !strings.HasSuffix(content, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String(), changed
}

// render interprets the marker spec (path, section or range, option) and returns the text to show.
func render(spec, cwd string, m *messages) (string, error) {
	fence := false
	if i := strings.LastIndex(spec, " | "); i >= 0 {
		opt := strings.TrimSpace(spec[i+3:])
		if opt != "raw" && opt != "囲む" {
			return "", fmt.Errorf(m.unknownOpt, opt)
		}
		fence = true
		spec = strings.TrimSpace(spec[:i])
	}

	path, heading, hasHeading := spec, "", false
	var from, to int
	hasRange := false
	if i := strings.Index(spec, "#"); i >= 0 {
		path = strings.TrimSpace(spec[:i])
		heading = strings.TrimSpace(strings.TrimLeft(spec[i:], "# \t"))
		hasHeading = true
		if heading == "" {
			return "", errors.New(m.emptyHeading)
		}
	} else if m := rangeRe.FindStringSubmatch(spec); m != nil {
		path = spec[:len(spec)-len(m[0])]
		from, _ = strconv.Atoi(m[1])
		to, _ = strconv.Atoi(m[2])
		hasRange = true
	}

	full, err := resolve(path, cwd, m)
	if err != nil {
		return "", err
	}
	text, err := readText(full, m)
	if err != nil {
		return "", err
	}
	lines := splitLines(text)

	switch {
	case hasHeading:
		lines, err = section(lines, heading, m)
	case hasRange:
		lines, err = lineRange(lines, from, to, m)
	}
	if err != nil {
		return "", err
	}
	body := strings.Join(lines, "\n")
	if fence {
		return wrapFence(body), nil
	}
	return body + "\n", nil
}

// frame puts divider lines before and after content, which ends with a line break.
// Blank lines around the dividers keep Markdown from joining them with the
// neighbouring paragraphs.
func frame(spec, content string, m *messages) string {
	return "\n" + fmt.Sprintf(m.begin, spec) + "\n\n" + content + "\n" + m.end + "\n\n"
}

// resolve makes path absolute and rejects files that may hold secrets.
func resolve(path, cwd string, m *messages) (string, error) {
	if path == "" {
		return "", errors.New(m.emptyPath)
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if denied(path) {
		return "", errors.New(m.denied)
	}
	// Check the link target too, so a symbolic link cannot get around the check.
	if real, err := filepath.EvalSymlinks(path); err == nil && denied(real) {
		return "", errors.New(m.denied)
	}
	return path, nil
}

// denied reports whether path looks like a file that holds secrets, judging by its name only.
func denied(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		p := strings.ToLower(part)
		if p == ".ssh" || p == ".aws" || p == ".gnupg" {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pfx"):
		return true
	case strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa"):
		return true
	case base == ".git-credentials" || base == ".netrc":
		return true
	}
	return false
}

func readText(path string, m *messages) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(m.notFound, path)
		}
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf(m.isDir, path)
	}
	if st.Size() > maxFileBytes {
		return "", fmt.Errorf(m.tooLarge, st.Size(), maxFileBytes)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(b) {
		return "", fmt.Errorf(m.notUTF8, path)
	}
	return string(b), nil
}

// splitLines normalizes line breaks to \n and splits text into lines, dropping the
// empty line that a trailing line break would leave.
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func lineRange(lines []string, from, to int, m *messages) ([]string, error) {
	if from < 1 || to < from {
		return nil, fmt.Errorf(m.badRange, from, to)
	}
	if from > len(lines) {
		return nil, fmt.Errorf(m.outOfRange, from, to, len(lines))
	}
	if to > len(lines) {
		to = len(lines)
	}
	return lines[from-1 : to], nil
}

// section returns the section whose heading text matches exactly, up to the next
// heading of the same or a higher level. # lines inside code blocks are not headings.
func section(lines []string, heading string, m *messages) ([]string, error) {
	type hd struct{ idx, level int }
	var hits, all []hd
	inFence := false
	for i, l := range lines {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := headingRe.FindStringSubmatch(l); m != nil {
			h := hd{i, len(m[1])}
			all = append(all, h)
			if m[2] == heading {
				hits = append(hits, h)
			}
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf(m.noHeading, heading)
	case 1:
	default:
		return nil, fmt.Errorf(m.dupHeading, len(hits), heading)
	}
	start := hits[0]
	end := len(lines)
	for _, h := range all {
		if h.idx > start.idx && h.level <= start.level {
			end = h.idx
			break
		}
	}
	return lines[start.idx:end], nil
}

// wrapFence wraps body in a code fence longer than any run of backticks inside it.
func wrapFence(body string) string {
	longest, run := 0, 0
	for _, r := range body {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := 3
	if longest >= n {
		n = longest + 1
	}
	f := strings.Repeat("`", n)
	return f + "\n" + body + "\n" + f + "\n"
}
