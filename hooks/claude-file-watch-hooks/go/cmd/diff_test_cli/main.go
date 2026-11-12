// Test only. Takes two text files and writes UnifiedDiff(n=1) joined with "\n" to
// stdout, for comparison with difflib in a fuzz test. Not used by the hooks.
package main

import (
	"os"

	fw "filewatch/internal/filewatch"
)

func main() {
	aBytes, _ := os.ReadFile(os.Args[1])
	bBytes, _ := os.ReadFile(os.Args[2])
	a := fw.SplitLines(string(aBytes))
	b := fw.SplitLines(string(bBytes))
	lines := fw.UnifiedDiff(a, b, 1)
	os.Stdout.WriteString(fw.JoinLines(lines))
}
