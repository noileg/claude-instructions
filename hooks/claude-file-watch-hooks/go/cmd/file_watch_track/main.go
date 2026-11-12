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
	if !textExts.Has(filePath) {
		trackHashOnly(sessionID, filePath, info)
		return
	}

	content, err := fw.ReadTextUTF8Replace(filePath)
	if err != nil {
		return
	}

	statePath := fw.StatePath(sessionID)
	state := fw.LoadStateMap(statePath)
	state[filePath] = fw.Snapshot{Hash: fw.Sha256Hex(content), Content: content}
	fw.WriteJSONAtomic(statePath, state)
}

// info is taken before hashing: if the file changes while it is hashed, the recorded
// modification time is older than the file's, and file_watch_check hashes it again.
func trackHashOnly(sessionID, filePath string, info os.FileInfo) {
	hash, err := fw.Sha256File(filePath)
	if err != nil {
		return
	}
	statePath := fw.HashOnlyStatePath(sessionID)
	state := fw.LoadHashOnlyMap(statePath)
	state[filePath] = fw.NewHashOnlySnapshot(hash, info)
	fw.WriteJSONAtomic(statePath, state)
}
