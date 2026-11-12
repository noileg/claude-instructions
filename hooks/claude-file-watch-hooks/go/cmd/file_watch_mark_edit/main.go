// PostToolUse (matcher: Edit|Write). Right after Edit/Write changes a file, records its
// hash in a global registry. file_watch_check compares against it to tell whether a
// detected change was made by Claude Code itself (the session is recorded too, since
// several sessions may run at once).
package main

import (
	"os"
	"path/filepath"

	fw "filewatch/internal/filewatch"
)

type hookInput struct {
	SessionID string `json:"session_id"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	ToolResponse struct {
		FilePath string `json:"filePath"`
	} `json:"tool_response"`
}

func main() {
	var data hookInput
	if err := fw.ReadStdinJSON(&data); err != nil {
		return
	}

	sessionID := data.SessionID
	if sessionID == "" {
		sessionID = "unknown"
	}
	filePath := data.ToolInput.FilePath
	if filePath == "" {
		filePath = data.ToolResponse.FilePath
	}
	if filePath == "" {
		return
	}
	if info, err := os.Stat(filePath); err != nil || info.IsDir() {
		return
	}

	content, err := fw.ReadTextUTF8Replace(filePath)
	if err != nil {
		return
	}

	registryPath := filepath.Join(fw.StateDir(), "claude_edits.json")
	registry := fw.LoadRegistryMap(registryPath)
	registry[filePath] = fw.EditEntry{Hash: fw.Sha256Hex(content), SessionID: sessionID}

	// Written atomically: parallel processes writing at the same time must not mix their content.
	fw.WriteJSONAtomic(registryPath, registry)
}
