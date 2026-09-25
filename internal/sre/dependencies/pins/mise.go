package pins

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// miseGoKey matches the go tool's key in any TOML quoting, and miseValue any tool's value as a
// string, as an inline table's own version key, or through a dotted .version key.
const (
	miseGoKey = `(?:go|"go"|'go')`
	// miseGoAlias names Go under another key: golang, a backend such as core:go or
	// aqua:golang/go, or a plugin path ending in golang, such as asdf:kennyp/asdf-golang. A
	// path ending in -go names a Go tool, such as protoc-gen-go, not Go itself. devex manages
	// only the canonical go key and warns about these. Aliases a project defines under
	// [tool_alias] go unnoticed.
	miseGoAlias = `(?:[\w-]+:)?(?:[\w.-]+/)*(?:go|(?:[\w.-]*-)?golang)`
	// miseGoAnyKey matches the go key or an alias, in any TOML quoting.
	miseGoAnyKey = `(?:go(?:lang)?|"` + miseGoAlias + `"|'` + miseGoAlias + `')`
	miseValue    = `(?:\.version)?\s*=\s*(?:\{(?:[^{}]*?,)?\s*version\s*=\s*)?` + tomlString
	// miseGoAssign follows the go key when a line assigns it a version in any form.
	miseGoAssign = `\s*(?:=|\.version\b)`
	// miseVersion starts the version line of a [tools.go] table.
	miseVersion = `^\s*version\s*=`
	// tomlString matches a quoted TOML string and captures its text.
	tomlString = `["']([^"']*)["']`
)

// misePatterns holds the pattern for a table's go pin line, and the one spotting a go pin in
// a form the first cannot manage. An alias table has no entry pattern.
type misePatterns struct{ entry, mention *regexp.Regexp }

var (
	tomlTableHeader = regexp.MustCompile(`^\s*\[\s*([^\]]+?)\s*\]`)
	tomlKeyQuotes   = strings.NewReplacer(`"`, "", "'", "", " ", "")
	// miseGoTables maps each table that can hold the go pin to its patterns: a dotted tools.go
	// key at the root, a go key under [tools], and version under [tools.go].
	miseGoTables = map[string]misePatterns{
		"": {
			regexp.MustCompile(`^\s*tools\.` + miseGoKey + miseValue),
			regexp.MustCompile(`(?:^|[\s{,]|\btools\.)` + miseGoAnyKey + miseGoAssign),
		},
		"tools": {
			regexp.MustCompile(`^\s*` + miseGoKey + miseValue),
			regexp.MustCompile(`(?:^|[\s{,])` + miseGoAnyKey + miseGoAssign),
		},
		"tools.go": {miseTableEntry, miseVersionKey},
	}
	// miseTableEntry matches the version line of a [tools.<key>] table.
	miseTableEntry = regexp.MustCompile(miseVersion + `\s*` + tomlString)
	miseVersionKey = regexp.MustCompile(miseVersion)
	// miseAliasTable serves a [tools.<alias>] table, such as [tools.golang].
	miseAliasTable = misePatterns{mention: miseVersionKey}
	miseGoToolKey  = regexp.MustCompile(`^` + miseGoAlias + `$`)
	// miseConfigName and miseDirConfigName match the file names mise loads, including their
	// environment and local variants, such as mise.ci.toml and config.ci.local.toml.
	miseConfigName    = regexp.MustCompile(`^\.?mise(?:\.[A-Za-z0-9_-]+){0,2}\.toml$`)
	miseDirConfigName = regexp.MustCompile(`^config(?:\.[A-Za-z0-9_-]+){0,2}\.toml$`)
)

// IsMiseGoKey reports whether key, unquoted, names Go itself in mise, such as go, golang, or
// core:go.
func IsMiseGoKey(key string) bool {
	return miseGoToolKey.MatchString(key)
}

// IsMiseConfig reports whether p names a file mise may load as config in some environment,
// such as mise.toml, mise.ci.toml, or .config/mise/conf.d/tasks.toml.
func IsMiseConfig(p string) bool {
	base, dir := path.Base(p), path.Dir(p)
	switch {
	case miseConfigName.MatchString(base):
		return true
	case miseDirConfigName.MatchString(base):
		return isMiseDir(dir)
	case path.Ext(base) == ".toml" && path.Base(dir) == "conf.d":
		return isMiseDir(path.Dir(dir))
	default:
		return false
	}
}

// IsMiseLock reports whether p is a mise lock file, which mise names after its config: mise.lock
// beside mise.toml, mise.ci.lock beside mise.ci.toml, and config.lock in a mise directory.
func IsMiseLock(p string) bool {
	lock, ok := strings.CutSuffix(p, ".lock")

	return ok && IsMiseConfig(lock+".toml")
}

func isMiseDir(dir string) bool {
	base := path.Base(dir)

	return base == "mise" || base == ".mise"
}

// MiseTool is one tool entry in a mise config whose version is a single string.
type MiseTool struct {
	// File is the slash-separated path relative to the repository root.
	File string
	// Line is the 1-based line holding the version.
	Line int
	// Key is the tool's key without quotes, such as "node" or "aqua:golangci/golangci-lint".
	Key string
	// Version is the version as written, such as "26.8.2", "v1.72.0", or "latest".
	Version string
	// Span locates Version within File.
	Span Span
}

// miseKey matches a TOML key in any quoting and captures it with its quotes.
const miseKey = `([A-Za-z0-9_-]+|"[^"]+"|'[^']+')`

var (
	miseToolsEntry = regexp.MustCompile(`^\s*` + miseKey + miseValue)
	miseRootEntry  = regexp.MustCompile(`^\s*tools\.` + miseKey + miseValue)
)

// MiseTools lists every tool entry in a mise config: keys under [tools], a root tools.<key>,
// and version under a [tools.<key>] table. Entries whose value is an array or a multi-line
// table are skipped, since no single string names their version.
func MiseTools(file string, data []byte) []MiseTool {
	var (
		tools []MiseTool
		state tomlState
		table string
	)
	for l := range numberedLines(data) {
		inherited := state
		var code string
		state, code = scanTOMLLine(l.text, state)
		if inherited != (tomlState{}) {
			continue
		}
		if m := tomlTableHeader.FindStringSubmatch(code); m != nil {
			table = tomlKeyQuotes.Replace(m[1])

			continue
		}

		// A [tools.<key>] table names its key in the header; the other forms name it on the line.
		// Either way, the version is the last capture group.
		re, key := miseToolsEntry, ""
		switch {
		case table == "":
			re = miseRootEntry
		case strings.HasPrefix(table, "tools."):
			re, key = miseTableEntry, strings.TrimPrefix(table, "tools.")
		case table != "tools":
			continue
		}
		m := re.FindStringSubmatchIndex(code)
		if m == nil {
			continue
		}
		if key == "" {
			key = tomlKeyQuotes.Replace(code[m[2]:m[3]])
		}
		start, end := m[len(m)-2], m[len(m)-1]
		tools = append(tools, MiseTool{
			File:    file,
			Line:    l.no,
			Key:     key,
			Version: code[start:end],
			Span:    Span{Start: l.start + start, End: l.start + end},
		})
	}

	return tools
}

func findMise(file string, data []byte) ([]Pin, []string) {
	var (
		pins     []Pin
		warnings []string
		state    tomlState
	)
	patterns, inGoTable := miseGoTables[""]
	for l := range numberedLines(data) {
		// A line continuing a multi-line string, such as a task script, or an array, such as a
		// row that looks like a table header, holds no key.
		inherited := state
		var code string
		state, code = scanTOMLLine(l.text, state)
		if inherited != (tomlState{}) {
			continue
		}

		if m := tomlTableHeader.FindStringSubmatch(code); m != nil {
			patterns, inGoTable = misePatternsFor(tomlKeyQuotes.Replace(m[1]))

			continue
		}
		if !inGoTable {
			continue
		}
		if patterns.entry != nil {
			if p, w := linePin(file, l, patterns.entry, KindMise); p != nil || w != nil {
				pins, warnings = append(pins, p...), append(warnings, w...)

				continue
			}
		}
		// A comment may mention go.mod or similar; only the text before it can hold a key.
		if patterns.mention.MatchString(code) {
			warnings = append(warnings, fmt.Sprintf("%s:%d: devex cannot manage this go pin; write it as go = \"<version>\" under [tools]", file, l.no))
		}
	}

	return pins, warnings
}

// misePatternsFor returns the patterns for a table that can hold a go pin, or false.
func misePatternsFor(table string) (misePatterns, bool) {
	if patterns, ok := miseGoTables[table]; ok {
		return patterns, true
	}
	if key, ok := strings.CutPrefix(table, "tools."); ok && IsMiseGoKey(key) {
		return miseAliasTable, true
	}

	return misePatterns{}, false
}

// tomlState is what a line of TOML inherits from the lines before it.
type tomlState struct {
	// open is the delimiter of a multi-line string still open.
	open string
	// depth counts the arrays and inline tables still open.
	depth int
}

// tomlQuotes maps each quote to its single-line and multi-line string delimiters.
var tomlQuotes = map[byte][2]string{'"': {`"`, `"""`}, '\'': {`'`, `'''`}}

// scanTOMLLine walks one line of TOML from state, and returns the state at the line's end
// and the line's text before any comment.
func scanTOMLLine(line string, state tomlState) (tomlState, string) {
	for i := 0; i < len(line); {
		if state.open != "" {
			end := closingDelimiter(line[i:], state.open)
			if end < 0 {
				return state, line
			}
			i += end + len(state.open)
			state.open = ""

			continue
		}
		switch c := line[i]; c {
		case '"', '\'':
			single, multi := tomlQuotes[c][0], tomlQuotes[c][1]
			if strings.HasPrefix(line[i:], multi) {
				state.open = multi
				i += len(multi)

				continue
			}
			end := closingDelimiter(line[i+1:], single)
			if end < 0 {
				return state, line
			}
			i += end + 2
		case '#':
			return state, line[:i]
		case '[', '{':
			state.depth++
			i++
		case ']', '}':
			state.depth = max(state.depth-1, 0)
			i++
		default:
			i++
		}
	}

	return state, line
}

// closingDelimiter returns the offset of delim in s, or -1. A backslash escape cannot close
// a basic string, one delimited by double quotes.
func closingDelimiter(s, delim string) int {
	for i := 0; i+len(delim) <= len(s); i++ {
		if delim[0] == '"' && s[i] == '\\' {
			i++

			continue
		}
		if strings.HasPrefix(s[i:], delim) {
			return i
		}
	}

	return -1
}
