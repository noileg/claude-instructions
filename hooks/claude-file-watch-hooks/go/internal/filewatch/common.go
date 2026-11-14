// Shared code for the file-watch hooks: where the state lives, atomic replacement, JSON I/O.
package filewatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Files with these extensions are tracked with their content, so changes are reported
// with a diff. Any other file (images, PDFs, .ipynb, which tend to be non-text or huge)
// is tracked by hash only: a change is reported, but not its content.
// This is the built-in list; -config replaces it and -config-add extends it (see ExtensionsFromArgs).
var TextExtensions = ExtensionSet{
	".txt": true, ".md": true, ".markdown": true,
	".py": true, ".pyw": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".json": true, ".jsonl": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".properties": true,
	".html": true, ".htm": true, ".hta": true, ".css": true, ".scss": true,
	".csv": true, ".tsv": true, ".srt": true,
	".ps1": true, ".sh": true, ".bash": true, ".bat": true, ".cmd": true, ".ahk": true,
	".java": true, ".kt": true, ".gradle": true, ".c": true, ".h": true, ".hpp": true, ".cpp": true, ".cs": true, ".go": true, ".rs": true, ".rb": true, ".php": true,
	".sql": true, ".xml": true, ".log": true, ".patch": true, ".gitignore": true, ".tasks": true,
}

// ExtensionSet is a set of lowercase extensions with the leading dot (".md").
type ExtensionSet map[string]bool

func (s ExtensionSet) Has(path string) bool {
	return s[strings.ToLower(filepath.Ext(path))]
}

// Why the extension config could not be used.
const (
	ConfigNotFound     = "not_found"
	ConfigUnreadable   = "unreadable"
	ConfigBadJSON      = "bad_json"
	ConfigNoExtensions = "no_extensions"
	ConfigBothGiven    = "both_given"
)

// ConfigError says that the extension config given with -config or -config-add could
// not be used. Reason is one of the Config* constants; Detail is the underlying error
// text, if any. Path is empty for ConfigBothGiven.
type ConfigError struct {
	Path   string
	Reason string
	Detail string
}

func (e *ConfigError) Error() string {
	if e.Detail != "" {
		return e.Reason + ": " + e.Detail
	}
	return e.Reason
}

// ArgValue returns the value that follows the flag name in args (os.Args-style, the
// program name first), and whether the flag was given with a value.
func ArgValue(args []string, name string) (string, bool) {
	for i := 1; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1], true
		}
	}
	return "", false
}

// LoadExtensionConfig reads {"extensions": [".txt", ".md", ...]}. Each element is
// lowercased and given a leading dot if it has none; empty elements are dropped.
// An empty array is valid.
func LoadExtensionConfig(path string) (ExtensionSet, *ConfigError) {
	b, err := os.ReadFile(path)
	if err != nil {
		reason := ConfigUnreadable
		if os.IsNotExist(err) {
			reason = ConfigNotFound
		}
		return nil, &ConfigError{Path: path, Reason: reason, Detail: err.Error()}
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, &ConfigError{Path: path, Reason: ConfigBadJSON, Detail: err.Error()}
	}
	raw, ok := top["extensions"]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil, &ConfigError{Path: path, Reason: ConfigNoExtensions}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, &ConfigError{Path: path, Reason: ConfigNoExtensions, Detail: err.Error()}
	}
	set := ExtensionSet{}
	for _, e := range list {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		set[e] = true
	}
	return set, nil
}

// ExtensionsFromArgs returns the extension set selected in args:
//
//	-config <json>      only the extensions in the JSON
//	-config-add <json>  the built-in TextExtensions plus the extensions in the JSON
//
// Without either (or with an empty value) it is the built-in TextExtensions. If the
// config cannot be used, or both flags are given, it is the built-in list too, and the
// error says why.
func ExtensionsFromArgs(args []string) (ExtensionSet, *ConfigError) {
	replacePath, _ := ArgValue(args, "-config")
	addPath, _ := ArgValue(args, "-config-add")
	switch {
	case replacePath != "" && addPath != "":
		return TextExtensions, &ConfigError{Reason: ConfigBothGiven}
	case replacePath != "":
		set, cerr := LoadExtensionConfig(replacePath)
		if cerr != nil {
			return TextExtensions, cerr
		}
		return set, nil
	case addPath != "":
		extra, cerr := LoadExtensionConfig(addPath)
		if cerr != nil {
			return TextExtensions, cerr
		}
		merged := ExtensionSet{}
		for e := range TextExtensions {
			merged[e] = true
		}
		for e := range extra {
			merged[e] = true
		}
		return merged, nil
	}
	return TextExtensions, nil
}

// FILE_WATCH_STATE_DIR redirects the state to another folder (for tests).
// Otherwise ~/.claude/tmp/file_watch is used.
func StateDir() string {
	if v := os.Getenv("FILE_WATCH_STATE_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".claude", "tmp", "file_watch")
}

// State layout. Each file has a state file of its own, named after the hash of its path,
// so hooks that run at the same time (parallel Reads, several sessions) write different
// files and never undo each other's writes. No locks are used:
//
//	{StateDir}/sessions/{session_id}/{sha256 of path}.json  a file read in that session
//	{StateDir}/claude_edits/{sha256 of path}.json           Claude Code's last write to a file
//
// A state file is only ever replaced whole (WriteEntry).
const (
	entryExt = ".json"
	tmpExt   = ".tmp"
)

func SessionDir(sessionID string) string {
	return filepath.Join(StateDir(), "sessions", sessionID)
}

func EditsDir() string {
	return filepath.Join(StateDir(), "claude_edits")
}

// EntryPath is the state file of path in dir. The path is hashed because it contains
// characters such as ':' and '\' and has no length limit.
func EntryPath(dir, path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+entryExt)
}

// Snapshot is a file as Claude Code last saw it. For a text file, Hash is over Content.
// For a file outside the text list (HashOnly), there is no Content, Hash is over the raw
// bytes (Sha256File), and Size and ModTime let file_watch_check skip hashing files whose
// size and modification time have not changed.
type Snapshot struct {
	Path     string `json:"path"`
	Hash     string `json:"hash"`
	Content  string `json:"content,omitempty"`
	HashOnly bool   `json:"hash_only,omitempty"`
	Size     int64  `json:"size,omitempty"`
	ModTime  int64  `json:"mtime_ns,omitempty"`
}

func TextSnapshot(path, content string) Snapshot {
	return Snapshot{Path: path, Hash: Sha256Hex(content), Content: content}
}

func HashOnlySnapshot(path, hash string, info os.FileInfo) Snapshot {
	return Snapshot{Path: path, Hash: hash, HashOnly: true, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

// EditEntry records Claude Code's last write to a file. Hash is over the UTF-8-cleaned
// text (ReadTextUTF8Replace), whatever the file type.
type EditEntry struct {
	Path      string `json:"path"`
	Hash      string `json:"hash"`
	SessionID string `json:"session_id"`
}

func Sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Hashes the raw bytes, without the UTF-8 cleanup of ReadTextUTF8Replace, so that
// every byte change in a non-text file counts. Streams, so large files are not loaded whole.
func Sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Reads a file as UTF-8, replacing invalid bytes with U+FFFD before anything else
// (hashing included), so hashes and diffs always see the same text.
func ReadTextUTF8Replace(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(b), "�"), nil
}

func ReadStdinJSON(v interface{}) error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func WriteStdoutJSON(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}

// LoadSession returns the snapshots of the files tracked in a session, sorted by path.
// A state file that cannot be read right now (for example while a hook is replacing it)
// is left out; it is still there for the next check.
func LoadSession(sessionID string) []Snapshot {
	dir := SessionDir(sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var result []Snapshot
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != entryExt {
			continue
		}
		var s Snapshot
		if readJSON(filepath.Join(dir, e.Name()), &s) != nil || s.Path == "" {
			continue
		}
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

// LoadEdit returns the record of Claude Code's last write to path, if there is one.
func LoadEdit(path string) (EditEntry, bool) {
	var e EditEntry
	if readJSON(EntryPath(EditsDir(), path), &e) != nil || e.Path != path {
		return EditEntry{}, false
	}
	return e, true
}

func readJSON(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// On Windows a file cannot be renamed over while another process has it open (for
// example file_watch_check reading it; checked on Windows 11 with Go 1.27), so a failed
// rename is tried again for a while. Variables so that tests can shorten the wait.
var (
	renameAttempts = 20
	renameWait     = 50 * time.Millisecond
)

// WriteEntry replaces the state file of path in dir with v: it writes a temporary file
// in dir and renames it over the state file, so a reader sees either the old or the new
// state file, never half of one.
//
// If the rename still fails after the retries, the old state file stays and the error
// is returned. No change is lost by that: a snapshot only ever holds a version Claude
// Code has seen, so the next check diffs from it, at worst repeating a change Claude Code
// already knows. For an edit record, the note "changed by Claude Code" may be missing.
func WriteEntry(dir, path string, v interface{}) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "*"+tmpExt)
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	_, err = f.Write(b)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		target := EntryPath(dir, path)
		for i := 0; i < renameAttempts; i++ {
			if i > 0 {
				time.Sleep(renameWait)
			}
			if err = os.Rename(tmpPath, target); err == nil {
				return nil
			}
		}
	}
	os.Remove(tmpPath)
	return err
}
