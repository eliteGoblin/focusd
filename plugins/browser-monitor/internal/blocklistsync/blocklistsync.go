// Package blocklistsync keeps the browser-guard blocklist in ONE place.
//
// The Go guard.DefaultBlocklist is the single source of truth. The Python util
// (utils/mac-browser-guard/browser_guard.py) has its BLOCKLIST *generated* from
// it (see ./gen, run via `go generate ./...`), so the two can never drift. A
// test (blocklistsync_test) reads the committed Python file and asserts it
// still matches guard.DefaultBlocklist — a hard failure if someone edits one
// side without regenerating.
package blocklistsync

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Markers delimit the generated region inside browser_guard.py. Everything
// between them (inclusive of both marker lines) is machine-owned.
const (
	BeginMarker = "# >>> BEGIN GENERATED BLOCKLIST"
	EndMarker   = "# >>> END GENERATED BLOCKLIST"

	BeginKeywordMarker = "# >>> BEGIN GENERATED KEYWORDS"
	EndKeywordMarker   = "# >>> END GENERATED KEYWORDS"
)

// regionRe matches a whole generated region: the BEGIN marker, through the END
// marker line (to end of that line). (?s) lets . span newlines.
func regionRe(begin, end string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)` + regexp.QuoteMeta(begin) + `.*?` + regexp.QuoteMeta(end) + `[^\n]*`)
}

var (
	blockRe   = regionRe(BeginMarker, EndMarker)
	keywordRe = regionRe(BeginKeywordMarker, EndKeywordMarker)
)

// entryRe pulls each double-quoted domain from the generated block. Anchored to
// list-entry lines (leading whitespace + a quoted string) so it can never pick
// up a quoted token that a future edit adds to a marker/comment line.
var entryRe = regexp.MustCompile(`(?m)^\s+"([^"]+)"`)

// RenderPython renders the marked, generated BLOCKLIST block (both markers
// included) from entries. Output is valid Python: a `BLOCKLIST = [ ... ]` list.
func RenderPython(entries []string) string {
	return renderRegion(BeginMarker, EndMarker, "BLOCKLIST",
		"plugins/browser-monitor guard.DefaultBlocklist", entries)
}

// RenderPythonKeywords renders the generated KEYWORDS block. Output is valid
// Python: a `KEYWORDS = [ ... ]` list. A host is killed if it CONTAINS any
// keyword (substring), so a domain-hopping target is caught across every mirror.
func RenderPythonKeywords(keywords []string) string {
	return renderRegion(BeginKeywordMarker, EndKeywordMarker, "KEYWORDS",
		"plugins/browser-monitor guard.DefaultKeywords", keywords)
}

// renderRegion renders a marked, generated Python list block (both markers
// included): a `<varName> = [ ... ]` list sourced from the named Go variable.
func renderRegion(begin, end, varName, source string, entries []string) string {
	var b strings.Builder
	b.WriteString(begin)
	b.WriteString(" — source of truth: " + source + ".\n")
	b.WriteString("# Do NOT edit by hand; run `go generate ./...` in plugins/browser-monitor. <<<\n")
	b.WriteString(varName + " = [\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "    %q,\n", e)
	}
	b.WriteString("]\n")
	b.WriteString(end + " <<<")
	return b.String()
}

// Splice replaces the existing generated BLOCKLIST region in src with block. It
// errors if the markers are absent (so a mangled target never silently no-ops).
func Splice(src []byte, block string) ([]byte, error) {
	return spliceRegion(src, blockRe, block, BeginMarker, EndMarker)
}

// SpliceKeywords replaces the existing generated KEYWORDS region in src with block.
func SpliceKeywords(src []byte, block string) ([]byte, error) {
	return spliceRegion(src, keywordRe, block, BeginKeywordMarker, EndKeywordMarker)
}

func spliceRegion(src []byte, re *regexp.Regexp, block, begin, end string) ([]byte, error) {
	if !re.Match(src) {
		return nil, fmt.Errorf("blocklistsync: markers %q..%q not found in target", begin, end)
	}
	return re.ReplaceAllLiteral(src, []byte(block)), nil
}

// Extract returns the BLOCKLIST entries found inside the generated region.
func Extract(src []byte) ([]string, error) {
	return extractRegion(src, blockRe)
}

// ExtractKeywords returns the KEYWORDS entries found inside the generated region.
func ExtractKeywords(src []byte) ([]string, error) {
	return extractRegion(src, keywordRe)
}

func extractRegion(src []byte, re *regexp.Regexp) ([]string, error) {
	region := re.Find(src)
	if region == nil {
		return nil, fmt.Errorf("blocklistsync: generated block not found (markers missing?)")
	}
	var out []string
	for _, m := range entryRe.FindAllSubmatch(region, -1) {
		out = append(out, string(m[1]))
	}
	return out, nil
}

// RepoPythonPath walks up from startDir to find the mac-browser-guard util,
// returning the path to browser_guard.py. The generator and the drift test both
// use it so they target the same file regardless of the working directory.
func RepoPythonPath(startDir string) (string, error) {
	rel := filepath.Join("utils", "mac-browser-guard", "browser_guard.py")
	dir := startDir
	for {
		cand := filepath.Join(dir, rel)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("blocklistsync: could not locate %s above %s", rel, startDir)
		}
		dir = parent
	}
}
