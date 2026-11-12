package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixture is a stand-in for ~/.claude and for the folder the setup exe sits in.
type fixture struct {
	t      *testing.T
	claude string
	src    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, claude: filepath.Join(root, "claude"), src: filepath.Join(root, "src")}
	for _, d := range []string{f.claude, f.src} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, exe := range []string{trackExe, markExe, checkExe} {
		if err := os.WriteFile(filepath.Join(f.src, exe), []byte("stub "+exe), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) settingsPath() string { return filepath.Join(f.claude, "settings.json") }

func (f *fixture) writeSettings(v interface{}) {
	f.t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.settingsPath(), b, 0644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) readSettings() map[string]interface{} {
	f.t.Helper()
	root, ok := loadSettings(f.settingsPath())
	if !ok {
		f.t.Fatal("settings.json could not be read back")
	}
	return root
}

// install runs the setup as the exe would with args (mode -install), against the fixture.
func (f *fixture) install(lang string, args ...string) string {
	f.t.Helper()
	opts, err := parseArgs(args)
	if err != nil {
		f.t.Fatalf("parseArgs(%q): %v", args, err)
	}
	return installInto(f.claude, f.src, lang, opts.config)
}

func (f *fixture) exe(name string) string { return filepath.Join(f.claude, "hooks", name) }

// commands returns the registered commands of the track, mark_edit and check hooks.
func (f *fixture) commands() (track, mark, check string) {
	f.t.Helper()
	hooks, _ := f.readSettings()["hooks"].(map[string]interface{})
	return commandOf(hooks, "PostToolUse", f.exe(trackExe)),
		commandOf(hooks, "PostToolUse", f.exe(markExe)),
		commandOf(hooks, "UserPromptSubmit", f.exe(checkExe))
}

func (f *fixture) wantCommands(track, mark, check string) {
	f.t.Helper()
	gt, gm, gc := f.commands()
	if gt != track || gm != mark || gc != check {
		f.t.Errorf("commands differ\n track: %s\n want:  %s\n mark:  %s\n want:  %s\n check: %s\n want:  %s",
			gt, track, gm, mark, gc, check)
	}
}

func (f *fixture) plain() (track, mark string) {
	return quote(f.exe(trackExe)), quote(f.exe(markExe))
}

func TestInstallNewWithConfig(t *testing.T) {
	f := newFixture(t)
	cfg := filepath.Join(f.claude, "ext.json")
	f.install("en", "-config", cfg)
	track, mark := f.plain()
	f.wantCommands(track+" -config "+quote(cfg), mark, quote(f.exe(checkExe))+" -lang en -config "+quote(cfg))
}

func TestInstallNewWithConfigAdd(t *testing.T) {
	f := newFixture(t)
	cfg := filepath.Join(f.claude, "ext.json")
	f.install("ja", "-config-add", cfg)
	track, mark := f.plain()
	f.wantCommands(track+" -config-add "+quote(cfg), mark, quote(f.exe(checkExe))+" -lang ja -config-add "+quote(cfg))
}

func TestInstallWithoutConfigIsAsBefore(t *testing.T) {
	f := newFixture(t)
	f.install("ja")
	track, mark := f.plain()
	f.wantCommands(track, mark, quote(f.exe(checkExe))+" -lang ja")
}

func TestRerunKeepsConfig(t *testing.T) {
	for _, flag := range []string{"-config", "-config-add"} {
		f := newFixture(t)
		cfg := filepath.Join(f.claude, "ext.json")
		f.install("en", flag, cfg)
		f.install("en")
		track, mark := f.plain()
		f.wantCommands(track+" "+flag+" "+quote(cfg), mark, quote(f.exe(checkExe))+" -lang en "+flag+" "+quote(cfg))
		// -lang is still rewritten on a re-run that keeps the config.
		f.install("ja")
		f.wantCommands(track+" "+flag+" "+quote(cfg), mark, quote(f.exe(checkExe))+" -lang ja "+flag+" "+quote(cfg))
	}
}

func TestOtherFlagReplacesTheExistingOne(t *testing.T) {
	f := newFixture(t)
	a := filepath.Join(f.claude, "a.json")
	b := filepath.Join(f.claude, "b.json")
	track, mark := f.plain()
	check := quote(f.exe(checkExe)) + " -lang en"

	f.install("en", "-config", a)
	f.install("en", "-config-add", b)
	f.wantCommands(track+" -config-add "+quote(b), mark, check+" -config-add "+quote(b))

	f.install("en", "-config", a)
	f.wantCommands(track+" -config "+quote(a), mark, check+" -config "+quote(a))
}

func TestSameFlagWithOtherPathReplacesPath(t *testing.T) {
	f := newFixture(t)
	a := filepath.Join(f.claude, "a.json")
	b := filepath.Join(f.claude, "b.json")
	track, mark := f.plain()
	f.install("en", "-config", a)
	f.install("en", "-config", b)
	f.wantCommands(track+" -config "+quote(b), mark, quote(f.exe(checkExe))+" -lang en -config "+quote(b))
}

func TestEmptyValueRemovesBoth(t *testing.T) {
	for _, args := range [][]string{
		{"-config", ""},
		{"-config-add", ""},
		{"-config", "", "-config-add", ""},
	} {
		for _, first := range []string{"-config", "-config-add"} {
			f := newFixture(t)
			f.install("en", first, filepath.Join(f.claude, "a.json"))
			f.install("en", args...)
			track, mark := f.plain()
			f.wantCommands(track, mark, quote(f.exe(checkExe))+" -lang en")
			// Removed for good: a later re-run without arguments does not bring it back.
			f.install("en")
			f.wantCommands(track, mark, quote(f.exe(checkExe))+" -lang en")
		}
	}
}

func TestBothFlagsIsAnErrorAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.install("en", "-config", filepath.Join(f.claude, "a.json"))
	before, err := os.ReadFile(f.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-lang", "en", "-config", "a.json", "-config-add", "b.json"},
		{"-config-add", "b.json", "-config", "a.json"},
		{"-config", "", "-config-add", "b.json"},
		{"-config", "a.json", "-config-add", ""},
	} {
		opts, err := parseArgs(args)
		if err == nil {
			// The exe reports the error and stops; only a nil error goes on to install.
			installInto(f.claude, f.src, "en", opts.config)
			t.Errorf("parseArgs(%q) should fail", args)
			continue
		}
		if !strings.Contains(err.Error(), "-config") || !strings.Contains(err.Error(), "同時") {
			t.Errorf("message should be bilingual and name the flags: %q", err)
		}
	}
	after, err := os.ReadFile(f.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("settings.json changed")
	}
}

func TestPathWithSpacesAndJapanese(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.claude, "設定 フォルダ", "my ext")
	cfg := filepath.Join(dir, "拡張子 一覧.json")
	f.install("ja", "-config-add", cfg)
	track, mark := f.plain()
	wantTrack := track + ` -config-add "` + cfg + `"`
	wantCheck := quote(f.exe(checkExe)) + ` -lang ja -config-add "` + cfg + `"`
	f.wantCommands(wantTrack, mark, wantCheck)

	// Read back from settings.json and kept through a re-run.
	gt, _, gc := f.commands()
	if got := splitCommand(gt); !reflect.DeepEqual(got, []string{f.exe(trackExe), "-config-add", cfg}) {
		t.Errorf("splitCommand(track) = %q", got)
	}
	if got := splitCommand(gc); !reflect.DeepEqual(got, []string{f.exe(checkExe), "-lang", "ja", "-config-add", cfg}) {
		t.Errorf("splitCommand(check) = %q", got)
	}
	f.install("ja")
	f.wantCommands(wantTrack, mark, wantCheck)
}

func TestHandWrittenArgumentsAreNormalisedNotLost(t *testing.T) {
	f := newFixture(t)
	f.install("en")
	root := f.readSettings()
	hooks, _ := root["hooks"].(map[string]interface{})
	// Unquoted path and an argument order the installer does not produce.
	setCommand(t, hooks, "PostToolUse", f.exe(trackExe), quote(f.exe(trackExe))+` -config C:\tools\ext.json`)
	setCommand(t, hooks, "UserPromptSubmit", f.exe(checkExe), quote(f.exe(checkExe))+` -config-add "D:\a b\x.json" -lang ja`)
	f.writeSettings(root)

	f.install("en")
	track, mark := f.plain()
	f.wantCommands(track+` -config "C:\tools\ext.json"`, mark, quote(f.exe(checkExe))+` -lang en -config-add "D:\a b\x.json"`)
}

func TestOtherHooksAndSettingsAreUntouched(t *testing.T) {
	f := newFixture(t)
	other := map[string]interface{}{
		"matcher": "Bash",
		"hooks": []interface{}{map[string]interface{}{
			"type": "command", "command": `"C:\other tools\guard.exe" --strict -config "x y.json"`, "timeout": float64(5),
		}},
	}
	otherPrompt := map[string]interface{}{
		"hooks": []interface{}{map[string]interface{}{"type": "command", "command": `python "C:\a\b.py"`}},
	}
	f.writeSettings(map[string]interface{}{
		"model": "opus",
		"hooks": map[string]interface{}{
			"PostToolUse":      []interface{}{other},
			"UserPromptSubmit": []interface{}{otherPrompt},
			"Stop":             []interface{}{other},
		},
	})
	cfg := filepath.Join(f.claude, "e.json")
	f.install("en", "-config", cfg)
	f.install("en")
	f.install("en", "-config-add", cfg)
	f.install("en", "-config", "")

	root := f.readSettings()
	if root["model"] != "opus" {
		t.Error("top-level setting lost")
	}
	hooks := root["hooks"].(map[string]interface{})
	if !reflect.DeepEqual(hooks["Stop"], []interface{}{other}) {
		t.Error("Stop hooks changed")
	}
	post := hooks["PostToolUse"].([]interface{})
	prompt := hooks["UserPromptSubmit"].([]interface{})
	if len(post) != 3 || !reflect.DeepEqual(post[0], other) {
		t.Errorf("PostToolUse: want the other entry first, then track and mark_edit, got %v", post)
	}
	if len(prompt) != 2 || !reflect.DeepEqual(prompt[0], otherPrompt) {
		t.Errorf("UserPromptSubmit: want the other entry first, then check, got %v", prompt)
	}
	// The other tool's -config is not mistaken for ours.
	track, mark := f.plain()
	f.wantCommands(track, mark, quote(f.exe(checkExe))+" -lang en")
}

func TestUnreadableSettingsAreNotOverwritten(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.settingsPath(), []byte("{ broken"), 0644); err != nil {
		t.Fatal(err)
	}
	f.install("en", "-config", filepath.Join(f.claude, "a.json"))
	b, _ := os.ReadFile(f.settingsPath())
	if string(b) != "{ broken" {
		t.Errorf("settings.json was rewritten: %q", b)
	}
}

func TestParseArgs(t *testing.T) {
	rel := filepath.Join("sub", "ext.json")
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want options
	}{
		{nil, options{mode: "-install"}},
		{[]string{"-lang", "ja"}, options{mode: "-install", lang: "ja"}},
		{[]string{"-lang", "ja", "-config", rel}, options{mode: "-install", lang: "ja", config: configChange{configSet, "-config", abs}}},
		{[]string{"-config-add", rel, "-lang", "en"}, options{mode: "-install", lang: "en", config: configChange{configSet, "-config-add", abs}}},
		{[]string{"-config", ""}, options{mode: "-install", config: configChange{Kind: configClear}}},
		{[]string{"-config-add", ""}, options{mode: "-install", config: configChange{Kind: configClear}}},
		{[]string{"-config", "", "-config-add", ""}, options{mode: "-install", config: configChange{Kind: configClear}}},
		{[]string{"-uninstall", "-config", "a", "-config-add", "b"}, options{mode: "-uninstall"}},
	}
	for _, c := range cases {
		got, err := parseArgs(c.args)
		if err != nil {
			t.Errorf("parseArgs(%q): %v", c.args, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseArgs(%q) = %+v, want %+v", c.args, got, c.want)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	cases := map[string][]string{
		``:                                   nil,
		`"C:\a b\x.exe"`:                     {`C:\a b\x.exe`},
		`"C:\a b\x.exe" -lang ja`:            {`C:\a b\x.exe`, "-lang", "ja"},
		`"x.exe"  -config  "" -config-add y`: {"x.exe", "-config", "", "-config-add", "y"},
		`x.exe -config "日本語 フォルダ\e.json"`: {"x.exe", "-config", `日本語 フォルダ\e.json`},
	}
	for in, want := range cases {
		if got := splitCommand(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func setCommand(t *testing.T, hooks map[string]interface{}, event, exe, command string) {
	t.Helper()
	_, hook := findHook(getEventArray(hooks, event), exe)
	if hook == nil {
		t.Fatalf("no %s entry for %s", event, exe)
	}
	hook["command"] = command
}
