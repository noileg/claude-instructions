// Shared code for the file-watch hooks: where the state lives, atomic replacement, JSON I/O.
package filewatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// Per-session state of files tracked with their content: {session_id}.json.
func StatePath(sessionID string) string {
	return filepath.Join(StateDir(), sessionID+".json")
}

// Per-session state of files tracked by hash only. It is a separate file so that
// {session_id}.json keeps the {hash, content} format other tools read and append to.
func HashOnlyStatePath(sessionID string) string {
	return filepath.Join(StateDir(), sessionID+".hash_only.json")
}

type Snapshot struct {
	Hash    string `json:"hash"`
	Content string `json:"content"`
}

// Hash is over the raw bytes (Sha256File). Size and ModTime let file_watch_check skip
// hashing files whose size and modification time have not changed.
type HashOnlySnapshot struct {
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime_ns"`
}

func NewHashOnlySnapshot(hash string, info os.FileInfo) HashOnlySnapshot {
	return HashOnlySnapshot{Hash: hash, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

type EditEntry struct {
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

func LoadStateMap(path string) map[string]Snapshot {
	result := map[string]Snapshot{}
	b, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return map[string]Snapshot{}
	}
	return result
}

func LoadHashOnlyMap(path string) map[string]HashOnlySnapshot {
	result := map[string]HashOnlySnapshot{}
	b, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return map[string]HashOnlySnapshot{}
	}
	return result
}

func LoadRegistryMap(path string) map[string]EditEntry {
	result := map[string]EditEntry{}
	b, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return map[string]EditEntry{}
	}
	return result
}

// os.Rename replaces an existing destination on Windows too (it uses MoveFileEx).
// Even when parallel processes write the same state file, others only ever see
// it before or after a write, never half-written.
func WriteJSONAtomic(path string, v interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmpPath, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
