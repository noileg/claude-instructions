// Standalone exe that sets up the Claude Code file-watch hooks. It is distributed
// as its own file and used by double-clicking it.
//
// Usage:
//
//	hooks_setup.exe [-install] [-lang en|ja] [-config <json>] [-config-add <json>]
//	hooks_setup.exe -uninstall
//
// -install (the default, e.g. on double-click) copies the three hook exes that sit
// next to this exe into ~/.claude/hooks and adds them to the hooks section of
// ~/.claude/settings.json. Entries that already run the same exes are updated.
// -uninstall removes those entries and the three exes.
// -lang sets the language of the notices file_watch_check adds to the context, and
// of this program's messages. Without it, -install asks with a dialog.
// -config / -config-add name a JSON file {"extensions": [...]} that replaces / extends
// the built-in list of extensions tracked with their content. The path is made absolute
// and added to the commands of file_watch_track and file_watch_check. Giving one drops
// the other from the registered commands; giving both is an error. Giving neither keeps
// what the registered commands already have; an empty value ("") removes both. The JSON
// file itself is not created here.
//
// Results are reported in a message box, because the console window of a
// double-clicked program closes before it can be read.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	trackExe = "file_watch_track.exe"
	markExe  = "file_watch_mark_edit.exe"
	checkExe = "file_watch_check.exe"
)

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		messageBox("Claude Code hook setup / Claude Codeフック設定", err.Error())
		return
	}
	lang := opts.lang
	switch opts.mode {
	case "-install":
		if lang != "en" && lang != "ja" {
			lang = askLanguage()
			if lang == "" {
				return
			}
		}
		messageBox(tr(lang, "Claude Code hook setup", "Claude Codeフック設定"), install(lang, opts.config))
	case "-uninstall":
		messageBox("Claude Code hook setup / Claude Codeフック設定", uninstall())
	}
}

// How the -config / -config-add arguments of the registered commands are set.
const (
	configKeep  = iota // neither flag was given: keep what the commands already have
	configSet          // put Flag and Path on the commands, dropping the other flag
	configClear        // remove both flags
)

type configChange struct {
	Kind int
	Flag string // "-config" or "-config-add" (configSet)
	Path string // absolute (configSet)
}

type options struct {
	mode   string
	lang   string
	config configChange
}

// parseArgs reads the command line (without the program name). It fails, with a
// bilingual message, when -config and -config-add are given together (unless both are
// empty, which removes both) or when a path cannot be made absolute.
func parseArgs(args []string) (options, error) {
	opts := options{mode: "-install"}
	var replace, add *string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-install", "-uninstall":
			opts.mode = a
		case "-lang", "-config", "-config-add":
			if i+1 >= len(args) {
				continue
			}
			v := args[i+1]
			i++
			switch a {
			case "-lang":
				opts.lang = v
			case "-config":
				replace = &v
			case "-config-add":
				add = &v
			}
		}
	}
	if opts.mode == "-uninstall" {
		return opts, nil
	}
	switch {
	case replace != nil && add != nil:
		if *replace == "" && *add == "" {
			opts.config = configChange{Kind: configClear}
			return opts, nil
		}
		return opts, errors.New("-config and -config-add cannot be given together. Nothing was changed.\n" +
			"-config と -config-add は同時に指定できません。何も変更していません。")
	case replace != nil:
		return withConfig(opts, "-config", *replace)
	case add != nil:
		return withConfig(opts, "-config-add", *add)
	}
	return opts, nil
}

func withConfig(opts options, flag, path string) (options, error) {
	if path == "" {
		opts.config = configChange{Kind: configClear}
		return opts, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return opts, errors.New("Could not make the path absolute: " + path + "\n" +
			"パスを絶対パスにできませんでした: " + path)
	}
	opts.config = configChange{Kind: configSet, Flag: flag, Path: abs}
	return opts, nil
}

func tr(lang, en, ja string) string {
	if lang == "ja" {
		return ja
	}
	return en
}

const (
	mbYesNoCancel  = 0x3
	mbIconQuestion = 0x20
	idYes          = 6
	idNo           = 7
)

// askLanguage asks which language the notices should use, with English as the
// default button. It returns "" when cancelled.
func askLanguage() string {
	r := messageBoxFlags("Claude Code hook setup / Claude Codeフック設定",
		"Which language should the hook notices use?\n"+
			"フックの通知を何語にしますか？\n\n"+
			"Yes / はい: English\n"+
			"No / いいえ: 日本語\n"+
			"Cancel / キャンセル: quit / 中止",
		mbYesNoCancel|mbIconQuestion)
	switch r {
	case idYes:
		return "en"
	case idNo:
		return "ja"
	}
	return ""
}

func messageBox(title, text string) {
	messageBoxFlags(title, text, 0)
}

func messageBoxFlags(title, text string, flags uintptr) int {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	titlePtr, err1 := syscall.UTF16PtrFromString(title)
	textPtr, err2 := syscall.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return 0
	}
	r, _, _ := proc.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
	return int(r)
}

func claudeDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(home, ".claude")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

func selfDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return filepath.Dir(exe), true
}

func copyFile(src, dst string) bool {
	in, err := os.Open(src)
	if err != nil {
		return false
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return false
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err == nil
}

// A missing file counts as new: returns an empty map and true. If the file has
// content but can't be parsed, returns false and the caller gives up without
// writing anything (so existing settings are never replaced by an empty map).
func loadSettings(path string) (map[string]interface{}, bool) {
	root := map[string]interface{}{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return root, true
		}
		return nil, false
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, false
	}
	return root, true
}

func saveSettings(path string, root map[string]interface{}) bool {
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return false
	}
	return os.Rename(tmp, path) == nil
}

func getEventArray(hooks map[string]interface{}, event string) []interface{} {
	v, ok := hooks[event]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	return arr
}

// findHook finds the entry whose hook runs exe (its quoted path, with or without
// arguments). It returns the entry index and the hook object, or -1 and nil.
func findHook(arr []interface{}, exe string) (int, map[string]interface{}) {
	quoted := quote(exe)
	for i, e := range arr {
		entry, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		innerHooks, ok := entry["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, ih := range innerHooks {
			cmdObj, ok := ih.(map[string]interface{})
			if !ok {
				continue
			}
			cmd, _ := cmdObj["command"].(string)
			if cmd == quoted || strings.HasPrefix(cmd, quoted+" ") {
				return i, cmdObj
			}
		}
	}
	return -1, nil
}

func quote(path string) string {
	return "\"" + path + "\""
}

// commandOf returns the command of the entry that runs exe, or "".
func commandOf(hooks map[string]interface{}, event, exe string) string {
	if _, hook := findHook(getEventArray(hooks, event), exe); hook != nil {
		cmd, _ := hook["command"].(string)
		return cmd
	}
	return ""
}

// splitCommand splits a command line at spaces, treating a double-quoted run as part
// of one word (without the quotes). Backslashes are literal, as in Windows paths.
func splitCommand(cmd string) []string {
	var words []string
	var cur strings.Builder
	inQuote, started := false, false
	for _, r := range cmd {
		switch {
		case r == '"':
			inQuote = !inQuote
			started = true
		case (r == ' ' || r == '\t') && !inQuote:
			if started {
				words = append(words, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		words = append(words, cur.String())
	}
	return words
}

// configArgs returns the " -config <path>" / " -config-add <path>" text to append to a
// command, for the change requested; oldCommand is the registered command, whose
// config arguments are kept when nothing was requested.
func configArgs(change configChange, oldCommand string) string {
	switch change.Kind {
	case configSet:
		return " " + change.Flag + " " + quote(change.Path)
	case configKeep:
		words := splitCommand(oldCommand)
		out := ""
		for i := 1; i+1 < len(words); i++ {
			if (words[i] == "-config" || words[i] == "-config-add") && words[i+1] != "" {
				out += " " + words[i] + " " + quote(words[i+1])
				i++
			}
		}
		return out
	}
	return ""
}

// registerHooks adds the three hooks, or updates the commands of the existing entries
// for the same exes.
func registerHooks(hooks map[string]interface{}, trackPath, markPath, checkPath, lang string, change configChange) {
	trackCmd := quote(trackPath) + configArgs(change, commandOf(hooks, "PostToolUse", trackPath))
	checkCmd := quote(checkPath) + " -lang " + lang + configArgs(change, commandOf(hooks, "UserPromptSubmit", checkPath))
	upsertEvent(hooks, "PostToolUse", "Read", trackPath, trackCmd)
	upsertEvent(hooks, "PostToolUse", "Edit|Write", markPath, quote(markPath))
	upsertEvent(hooks, "UserPromptSubmit", "", checkPath, checkCmd)
}

// upsertEvent adds an entry that runs command, or updates the command of an
// existing entry for the same exe (e.g. to change -lang).
func upsertEvent(hooks map[string]interface{}, event, matcher, exe, command string) {
	arr := getEventArray(hooks, event)
	if _, hook := findHook(arr, exe); hook != nil {
		hook["command"] = command
		return
	}
	entry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
				"timeout": 10,
			},
		},
	}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	hooks[event] = append(arr, entry)
}

func removeEvent(hooks map[string]interface{}, event, exe string) {
	arr := getEventArray(hooks, event)
	if arr == nil {
		return
	}
	idx, _ := findHook(arr, exe)
	if idx < 0 {
		return
	}
	hooks[event] = append(arr[:idx], arr[idx+1:]...)
}

func install(lang string, change configChange) string {
	dir, ok := claudeDir()
	if !ok {
		return tr(lang,
			"Claude Code was not found (~/.claude does not exist).\nInstall Claude Code first, then run this again.",
			"Claude Codeが見つかりませんでした(~/.claude が存在しません)。\n先にClaude Codeを導入してから、もう一度実行してください。")
	}
	src, ok := selfDir()
	if !ok {
		return tr(lang, "Could not determine where this exe is; nothing was changed.",
			"自分自身の場所が特定できず、中止しました。")
	}
	return installInto(dir, src, lang, change)
}

// installInto copies the hook exes from src into dir/hooks and registers them in
// dir/settings.json.
func installInto(dir, src, lang string, change configChange) string {
	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return tr(lang, "Could not create ~/.claude/hooks: ", "~/.claude/hooks を作成できませんでした: ") + err.Error()
	}
	trackPath := filepath.Join(hooksDir, trackExe)
	markPath := filepath.Join(hooksDir, markExe)
	checkPath := filepath.Join(hooksDir, checkExe)
	okTrack := copyFile(filepath.Join(src, trackExe), trackPath)
	okMark := copyFile(filepath.Join(src, markExe), markPath)
	okCheck := copyFile(filepath.Join(src, checkExe), checkPath)
	if !okTrack || !okMark || !okCheck {
		return tr(lang,
			"Failed to copy the hook exes (they may be missing from the folder of this exe:\n"+
				trackExe+" / "+markExe+" / "+checkExe+").\nsettings.json was not changed.",
			"フック本体のコピーに失敗しました(このexeと同じフォルダに\n"+
				trackExe+" / "+markExe+" / "+checkExe+" が無い可能性があります)。\n"+
				"settings.jsonは変更していません。")
	}

	settingsPath := filepath.Join(dir, "settings.json")
	root, ok := loadSettings(settingsPath)
	if !ok {
		return tr(lang,
			"Could not read settings.json.\nIt may be broken, so to be safe nothing was changed.\nPlease check the file: ",
			"settings.json の内容を読み取れませんでした。\n壊れている可能性があるため、安全のため設定は変更していません。\nファイルを確認してください: ") + settingsPath
	}
	hooksVal, ok := root["hooks"].(map[string]interface{})
	if !ok {
		hooksVal = map[string]interface{}{}
	}

	registerHooks(hooksVal, trackPath, markPath, checkPath, lang, change)

	root["hooks"] = hooksVal
	if !saveSettings(settingsPath, root) {
		return tr(lang, "Failed to write settings.json.", "settings.json への書き込みに失敗しました。")
	}
	msg := tr(lang,
		"Done. When a file you are working on is changed outside Claude Code, you will be told on your next message.",
		"設定しました。編集中のファイルが外部で変更されたとき、次の発言時に通知されます。")
	switch change.Kind {
	case configSet:
		msg += "\n\n" + tr(lang, "Extension list: ", "拡張子一覧: ") + change.Flag + " " + change.Path
	case configClear:
		msg += "\n\n" + tr(lang, "Extension list: built-in", "拡張子一覧: 組み込み")
	}
	return msg
}

func uninstall() string {
	dir, ok := claudeDir()
	if !ok {
		return "Claude Code was not found (~/.claude does not exist).\n" +
			"Claude Codeが見つかりませんでした(~/.claude が存在しません)。"
	}
	hooksDir := filepath.Join(dir, "hooks")
	trackPath := filepath.Join(hooksDir, trackExe)
	markPath := filepath.Join(hooksDir, markExe)
	checkPath := filepath.Join(hooksDir, checkExe)

	settingsPath := filepath.Join(dir, "settings.json")
	if root, ok := loadSettings(settingsPath); ok {
		if hooksVal, ok := root["hooks"].(map[string]interface{}); ok {
			removeEvent(hooksVal, "PostToolUse", trackPath)
			removeEvent(hooksVal, "PostToolUse", markPath)
			removeEvent(hooksVal, "UserPromptSubmit", checkPath)
			root["hooks"] = hooksVal
			saveSettings(settingsPath, root)
		}
	}

	os.Remove(trackPath)
	os.Remove(markPath)
	os.Remove(checkPath)
	return "Removed the hook settings.\nフック設定を削除しました。"
}
