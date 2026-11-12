# claude-file-show-hook

日本語: [README.ja.md](README.ja.md)

A Claude Code hook that shows you the contents of a file in the conversation, without
Claude retyping it and without putting it into Claude's context.

## Why

When you ask Claude Code to show you a file (a draft, a section of a spec), it either
retypes the text in its reply, which spends output tokens and can introduce copying
mistakes, or prints the file with a tool, which puts the whole text into its context.
Opening the file in another editor means leaving the conversation.

This hook uses the `MessageDisplay` hook event, which is display-only: what the hook
returns changes only what is rendered on screen. Claude writes one marker line, and the
hook replaces that line on your screen with the part of the file it points to. The
transcript and Claude's context keep only the marker line.

## What it looks like

Claude's reply contains:

```
Here is the schedule section:

[[show: C:\work\plan.md#Schedule]]

Shall I change the second item?
```

On your screen it becomes:

```
Here is the schedule section:

──── begin: `C:\work\plan.md#Schedule` ────

## Schedule
- Draft by Friday
- Review on Monday

──── end ────

Shall I change the second item?
```

The contents are rendered as Markdown, tables included. The divider lines show where the
file's text starts and ends.

## Markers

A marker works only when it starts at the beginning of a line and the line holds nothing
else.

| Shows | Marker |
|---|---|
| Whole file | `[[show: path]]` |
| Section under a heading | `[[show: path#heading]]` |
| Line range (1-based, inclusive) | `[[show: path:10-40]]` |
| Verbatim, in a code block | add ` \| raw` at the end, e.g. `[[show: path#heading \| raw]]` |

`[[見せる: …]]` and ` | 囲む` are accepted as well, whatever `-lang` is.

- A section runs from the heading whose text matches exactly to just before the next
  heading of the same or a higher level. `#` lines inside code blocks are not headings.
  `path##heading` (with the `#` marks of the heading) works too. If two headings have the
  same text, the marker fails.
- Relative paths are resolved from the session's working directory. `~/` is your home
  directory.
- To write a marker without triggering it, for example when explaining the syntax,
  indent the line.

## Install

The easiest way is to give this README to Claude Code and ask it to install the hook.
In auto mode, Claude Code may decline to edit its own settings; in that case do step 2
by hand.

1. Download `claude-file-show-hook.exe` from
   [Releases](https://github.com/noileg/claude-file-show-hook/releases) (or build it, see
   below) and put it somewhere, for example `~/.claude/hooks/`.
2. Add a `MessageDisplay` entry to `hooks` in `~/.claude/settings.json`. `command` is the
   absolute path of the exe:

   ```json
   "MessageDisplay": [
     {
       "hooks": [
         {
           "type": "command",
           "command": "C:\\Users\\you\\.claude\\hooks\\claude-file-show-hook.exe",
           "args": []
         }
       ]
     }
   ]
   ```

   For Japanese divider lines and failure messages, use `"args": ["-lang", "ja"]`.
3. Tell Claude about the markers in your `CLAUDE.md` (`~/.claude/CLAUDE.md` applies to all
   projects). For example:

   ```markdown
   When you show me the contents of a file or part of it, don't retype it and don't print
   it with a tool. Write a line of its own: `[[show: <absolute path>#<heading>]]` (a line
   range is `<path>:10-40`, the whole file is the path alone, and ` | raw` at the end shows
   it verbatim). A MessageDisplay hook replaces that line with the file's contents on my
   screen only; you never see the contents. When you write the syntax in an explanation,
   indent the line so the hook leaves it alone.
   ```

## When a marker fails

If the file cannot be shown, the marker line is replaced with `[show failed: reason]`,
for example `file not found`, `heading not found`, `2 headings named "…"` or
`line range out of bounds`. A marker line left as it is would look the same as a hook
that is not installed, so a failure is always shown.

Files that may hold secrets are never shown. They are judged by name only: `.env`,
`.env.*`, `*.pem`, `*.key`, `*.pfx`, `id_rsa*`, `id_ed25519*`, `id_ecdsa*`,
`.git-credentials`, `.netrc`, and anything under `.ssh`, `.aws` or `.gnupg`. The target of
a symbolic link is checked as well. Markers come from Claude's output, so this keeps such
files off your screen whatever Claude is led to write.

Files over 5 MB and files that are not UTF-8 are reported as failures. A UTF-8 BOM is
dropped.

## Limits

- The contents are shown only when a reply is first displayed. If you reopen a session
  with `--resume`, past replies show their marker lines instead of the contents; ask
  Claude to write the marker again. Scrolling back within the session keeps the contents.
- Verbose mode shows the original marker lines.
- Requires Claude Code 2.1.152 or later, the version that added `MessageDisplay`.
- Tried with Claude Code 2.1.309 on Windows 11: about 49,000 characters (1,250 lines) were
  shown without truncation, and Markdown tables rendered as tables. Longer contents have
  not been tried.
- The prebuilt exe is for Windows (amd64). The code is plain Go and should build on macOS
  and Linux, but it has not been tried there.
- Claude Code runs the hook for every batch of lines it displays and waits for it. A run
  takes about 10 ms on Windows, including process start.

## Build

Requires Go 1.27 or later.

```
go build -trimpath -o claude-file-show-hook.exe .
go test ./...
```

`-trimpath` keeps the file paths of the build machine out of the exe.

## License

MIT. See [LICENSE](LICENSE).
