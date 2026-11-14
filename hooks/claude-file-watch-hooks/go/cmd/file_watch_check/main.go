// UserPromptSubmit. On every user message, checks whether the files tracked in this
// session have changed and, if so, adds their diffs to the context. If a change was
// made by Claude Code (Edit/Write), the record left by file_watch_mark_edit says so,
// and the notice tells this session's changes from other sessions'.
// Files tracked by hash only are reported as changed, without content.
//
// -lang en|ja selects the language of the notice (English by default).
//
// -config / -config-add are the extension list options of file_watch_track. This hook
// does not use the list itself (the state files say how each file is tracked); it only
// reports, on every message, when the config given to it cannot be used.
package main

import (
	"os"
	"strings"

	fw "filewatch/internal/filewatch"
)

const maxDiffLines = 200

type hookInput struct {
	SessionID string `json:"session_id"`
}

type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// lang is the language of the notice, set with -lang by hooks_setup.
func langFromArgs() string {
	if v, _ := fw.ArgValue(os.Args, "-lang"); v == "ja" {
		return "ja"
	}
	return "en"
}

// configNotice is the line that says the extension config could not be used and the
// built-in list is in effect instead.
func configNotice(lang string, cerr *fw.ConfigError) string {
	if cerr.Reason == fw.ConfigBothGiven {
		return tr(lang,
			"(file_watch: both -config and -config-add are given, so the built-in extension list is in use.)",
			"(file_watch: -config と -config-add が両方指定されているため、組み込みの拡張子一覧で動作しています)")
	}
	var reasonEn, reasonJa string
	switch cerr.Reason {
	case fw.ConfigNotFound:
		reasonEn, reasonJa = "the file does not exist", "ファイルが存在しません"
	case fw.ConfigUnreadable:
		reasonEn, reasonJa = "the file cannot be read: "+cerr.Detail, "ファイルを読み取れません: "+cerr.Detail
	case fw.ConfigBadJSON:
		reasonEn, reasonJa = "it is not valid JSON: "+cerr.Detail, "JSONとして壊れています: "+cerr.Detail
	default:
		reasonEn, reasonJa = `"extensions" is missing or is not an array of strings`, `"extensions" が無いか、文字列の配列ではありません`
		if cerr.Detail != "" {
			reasonEn += ": " + cerr.Detail
			reasonJa += ": " + cerr.Detail
		}
	}
	return tr(lang,
		"(file_watch: the config file "+cerr.Path+" cannot be used ("+reasonEn+"), so the built-in extension list is in use.)",
		"(file_watch: 設定ファイル "+cerr.Path+" を読めないため、組み込みの拡張子一覧で動作しています("+reasonJa+"))")
}

func tr(lang, en, ja string) string {
	if lang == "ja" {
		return ja
	}
	return en
}

func editSourceNote(lang, filePath, currentHash, sessionID string) string {
	entry, ok := fw.LoadEdit(filePath)
	if !ok || entry.Hash != currentHash {
		return ""
	}
	return sourceNoteFor(lang, entry, sessionID)
}

func sourceNoteFor(lang string, entry fw.EditEntry, sessionID string) string {
	if entry.SessionID == sessionID {
		return tr(lang, " (changed by Claude Code in this session)", "（Claude Code・このセッション自身による変更）")
	}
	return tr(lang, " (changed by Claude Code in another session)", "（Claude Code・別セッションによる変更）")
}

// The edit record holds the hash of the UTF-8-cleaned text (as file_watch_mark_edit
// records it), not the raw-byte hash kept for hash-only files, so it is computed
// here, and only when there is a record for the file at all.
func hashOnlyEditSourceNote(lang, filePath, sessionID string) string {
	entry, ok := fw.LoadEdit(filePath)
	if !ok {
		return ""
	}
	text, err := fw.ReadTextUTF8Replace(filePath)
	if err != nil || entry.Hash != fw.Sha256Hex(text) {
		return ""
	}
	return sourceNoteFor(lang, entry, sessionID)
}

func main() {
	lang := langFromArgs()
	configLine := ""
	if _, cerr := fw.ExtensionsFromArgs(os.Args); cerr != nil {
		configLine = configNotice(lang, cerr)
	}

	var data hookInput
	var changedBlocks []string
	var updates []fw.Snapshot
	sessionID := ""
	if err := fw.ReadStdinJSON(&data); err == nil {
		sessionID = data.SessionID
		if sessionID == "" {
			sessionID = "default"
		}
		changedBlocks, updates = changedFileBlocks(lang, sessionID)
	}
	// The snapshots are updated only after the notice is written, so that a check that is
	// stopped before writing its notice leaves the old snapshots, and the changes are
	// reported again next time instead of being lost.
	defer saveSnapshots(sessionID, updates)

	var context string
	if len(changedBlocks) > 0 {
		context = tr(lang,
			"(Files tracked in this session have changed. Unless noted, the change was made "+
				"outside Claude Code (e.g. by the user). The user's next message may be about these changes.)\n\n",
			"(このセッションで追跡しているファイルの中身が変わっています。"+
				"注記がなければClaude Code以外(本人の編集など)による変更です。"+
				"直後のユーザー発言は、この変更内容についてのものかもしれません)\n\n") +
			strings.Join(changedBlocks, "\n\n")
	}
	if configLine != "" {
		if context != "" {
			context += "\n\n"
		}
		context += configLine
	}
	if context == "" {
		return
	}

	fw.WriteStdoutJSON(hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "UserPromptSubmit",
			AdditionalContext: context,
		},
	})
}

// saveSnapshots replaces the snapshots of the files whose changes were reported (and of
// hash-only files that were only touched). A snapshot that cannot be replaced keeps its
// old version; the change is then reported again on the next message.
func saveSnapshots(sessionID string, updates []fw.Snapshot) {
	dir := fw.SessionDir(sessionID)
	for _, s := range updates {
		fw.WriteEntry(dir, s.Path, s)
	}
}

// changedFileBlocks compares the session's snapshots with the files as they are now and
// returns one block per changed file, with the snapshots to save afterwards.
func changedFileBlocks(lang, sessionID string) ([]string, []fw.Snapshot) {
	var changedBlocks []string
	var updates []fw.Snapshot

	// LoadSession sorts by path, so the output order is stable.
	for _, snap := range fw.LoadSession(sessionID) {
		filePath := snap.Path
		info, err := os.Stat(filePath)
		if err != nil || info.IsDir() {
			continue
		}

		if snap.HashOnly {
			if info.Size() == snap.Size && info.ModTime().UnixNano() == snap.ModTime {
				continue
			}
			currentHash, err := fw.Sha256File(filePath)
			if err != nil {
				continue
			}
			// Saved even when nothing is reported: a file that was only touched gets its new
			// size and modification time, so it is not hashed again on every message.
			updates = append(updates, fw.HashOnlySnapshot(filePath, currentHash, info))
			if currentHash == snap.Hash {
				continue
			}

			sourceNote := hashOnlyEditSourceNote(lang, filePath, sessionID)
			header := "### " + filePath + sourceNote
			changedBlocks = append(changedBlocks, header+"\n"+tr(lang,
				"(Not a format tracked as text, so only the fact that it changed is reported; no diff is shown.)",
				"(テキストとして追跡する形式ではないため、変わったことだけを報告します。差分は表示しません)"))
			continue
		}

		current, err := fw.ReadTextUTF8Replace(filePath)
		if err != nil {
			continue
		}

		currentHash := fw.Sha256Hex(current)
		if currentHash == snap.Hash {
			continue
		}

		oldLines := fw.SplitLines(snap.Content)
		newLines := fw.SplitLines(current)
		diffLines := fw.UnifiedDiff(oldLines, newLines, 1)

		var diffText string
		if len(diffLines) > maxDiffLines {
			diffText = fw.JoinLines(diffLines[:maxDiffLines]) + tr(lang, "\n(...diff truncated)", "\n(...差分が長いため省略)")
		} else {
			diffText = fw.JoinLines(diffLines)
		}

		sourceNote := editSourceNote(lang, filePath, currentHash, sessionID)
		header := "### " + filePath + sourceNote
		changedBlocks = append(changedBlocks, header+"\n```diff\n"+diffText+"\n```")

		updates = append(updates, fw.TextSnapshot(filePath, current))
	}

	return changedBlocks, updates
}
