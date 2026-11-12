# claude-file-watch-hooks

日本語: [README.ja.md](README.ja.md)

Claude Code hooks that notice when a file Claude Code is working on is changed by
something else (your own edits, another tool). Written in Go.

A Gist can't hold folders, so file names stand in for the original paths
(`cmd_file_watch_track_main.go` is `go/cmd/file_watch_track/main.go`,
`internal_filewatch_common.go` is `go/internal/filewatch/common.go`). To build
locally, put the files back into that folder layout.

## What it does

- `file_watch_track` (PostToolUse, matcher=Read): saves a snapshot of each file Claude Code reads, per session. Files in the text list are saved with their content; any other file is saved by hash only (with its size and modification time)
- `file_watch_mark_edit` (PostToolUse, matcher=Edit|Write): records the hash of each file Claude Code edits or writes, together with the session that wrote it
- `file_watch_check` (UserPromptSubmit): on every user message, compares the snapshots with the current contents and adds the diffs of changed files to the context. For files saved by hash only, it reports that they changed, without content or diff (it hashes them only when their size or modification time has changed). Changes made by Claude Code itself are matched against the records and noted as "this session" or "another session"
- `hooks_setup`: copies the three exes above into `~/.claude/hooks/` and registers them in the hooks section of `~/.claude/settings.json` (double-click to install, `-uninstall` to remove). Existing entries for the same exes are updated; `-config` / `-config-add` given to it (see below) are added to the commands of `file_watch_track` and `file_watch_check`

## Notice language

The notice `file_watch_check` adds to the context is in English (the default) or
Japanese. `hooks_setup` asks which one with a dialog (or pass
`hooks_setup.exe -lang ja` to skip the question). The choice is kept as the `-lang`
argument of the command registered in settings.json.

## Choosing which extensions get a diff

By default, the files shown as diffs are the built-in list (`TextExtensions` in
`internal/filewatch/common.go`). A JSON file can replace or extend it:

```json
{"extensions": [".txt", ".md", ".foo"]}
```

Each element is lowercased and gets a leading `.` if it has none. An empty array is
valid.

- `-config <json>`: use only the extensions in the JSON (replaces the built-in list). With an empty array, every file is tracked by hash only
- `-config-add <json>`: use the built-in list plus the extensions in the JSON. With an empty array, it is the same as the built-in list

Both are arguments of `file_watch_track` and `file_watch_check`. The language is not
in the JSON; it is still `-lang`.

If the file cannot be used (it does not exist, cannot be read, is not valid JSON, has
no `extensions` array of strings, or both `-config` and `-config-add` are given), the
built-in list is used. `file_watch_track` carries on silently; `file_watch_check` adds
one line to the context on every message, saying which file could not be used and why
(in the `-lang` language), even when there is nothing else to report.

`hooks_setup.exe -config <path>` or `-config-add <path>` turns the path into an
absolute path and adds it to the registered commands of `file_watch_track` and
`file_watch_check` (paths with spaces or Japanese are quoted). Giving one removes the
other from the registered commands; giving both is an error and changes nothing.
Running it again without either keeps what the registered commands already have, and
`-config ""` (or `-config-add ""`) removes both. `hooks_setup` does not create the JSON
file.

## Build

Requires Go 1.27 or later. In `go/`:

```
go build -trimpath -o bin/file_watch_track.exe ./cmd/file_watch_track
go build -trimpath -o bin/file_watch_mark_edit.exe ./cmd/file_watch_mark_edit
go build -trimpath -o bin/file_watch_check.exe ./cmd/file_watch_check
go build -trimpath -o bin/hooks_setup.exe ./cmd/hooks_setup
```

`-trimpath` keeps the file paths of the build machine out of the exes.

## Notes

Windows only. State is kept in `~/.claude/tmp/file_watch`: `{session_id}.json` for files tracked with their content, `{session_id}.hash_only.json` for the others.
By default only common text formats such as `.txt` / `.md` / `.py` are shown as diffs (`TextExtensions` in `internal/filewatch/common.go`; see "Choosing which extensions get a diff"). For any other file (images, PDFs, `.ipynb` and so on), only the fact that it changed is reported.

Prebuilt exes (`hooks_setup.exe` plus the three `file_watch_*.exe` that go next to it)
are at the top of https://github.com/noileg/croco-editor.
