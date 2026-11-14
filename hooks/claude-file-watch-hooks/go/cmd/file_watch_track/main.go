// PostToolUse (matcher: Read). Every time the Read tool reads a file, saves a snapshot
// of its content per session. file_watch_check compares against it to detect external changes.
// Files outside the text extension list are snapshotted by hash only, so their changes
// are reported without content.
//
// -config <json path> replaces the built-in list with {"extensions": [...]};
// -config-add <json path> adds those extensions to it. If the file cannot be used, the
// built-in list is used and nothing is reported here (file_watch_check reports it).
package main

import (
	"os"

	fw "filewatch/internal/filewatch"
)

type hookInput struct {
	SessionID string `json:"session_id"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

func main() {
	textExts, _ := fw.ExtensionsFromArgs(os.Args)

	var data hookInput
	if err := fw.ReadStdinJSON(&data); err != nil {
		return
	}

	sessionID := data.SessionID
	if sessionID == "" {
		sessionID = "default"
	}
	filePath := data.ToolInput.FilePath
	if filePath == "" {
		return
	}
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		return
	}
	dir := fw.SessionDir(sessionID)
	if !textExts.Has(filePath) {
		// info is taken before hashing: if the file changes while it is hashed, the recorded
		// modification time is older than the file's, and file_watch_check hashes it again.
		hash, err := fw.Sha256File(filePath)
		if err != nil {
			return
		}
		fw.WriteEntry(dir, filePath, fw.HashOnlySnapshot(filePath, hash, info))
		return
	}

	content, err := fw.ReadTextUTF8Replace(filePath)
	if err != nil {
		return
	}
	fw.WriteEntry(dir, filePath, fw.TextSnapshot(filePath, content))
}
