package pins

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

var (
	goVersionLine    = regexp.MustCompile(`^\s*(\S+)`)
	toolVersionsLine = regexp.MustCompile(`^\s*(?:golang|go)\s+(\S+)`)
)

func findGoVersionFile(file string, data []byte) ([]Pin, []string) {
	for l := range numberedLines(data) {
		if trimmed := strings.TrimSpace(l.text); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return linePin(file, l, goVersionLine, KindGoVersion)
		}
	}

	return nil, nil
}

func findToolVersions(file string, data []byte) ([]Pin, []string) {
	for l := range numberedLines(data) {
		if pins, warnings := linePin(file, l, toolVersionsLine, KindToolVersions); pins != nil || warnings != nil {
			return pins, warnings
		}
	}

	return nil, nil
}

// findGoMod reads go.mod's go and toolchain directives. A go.mod that fails to parse is an
// error: skipping it would drop its floor from every check.
func findGoMod(file string, data []byte) (finding, error) {
	f, err := modfile.Parse(file, data, nil)
	if err != nil {
		return finding{}, fmt.Errorf("parse %s: %w", file, err)
	}
	pins, warnings := directivePins(file, data, f.Go, f.Toolchain, KindGoDirective, KindGoToolchain)

	return finding{pins: pins, warnings: warnings}, nil
}

// findGoWork reads go.work's go and toolchain directives, which bind every module in the
// workspace, and the module directories its use directives name.
func findGoWork(file string, data []byte) (finding, error) {
	f, err := modfile.ParseWork(file, data, nil)
	if err != nil {
		return finding{}, fmt.Errorf("parse %s: %w", file, err)
	}
	pins, warnings := directivePins(file, data, f.Go, f.Toolchain, KindWorkGo, KindWorkToolchain)
	uses := make([]string, 0, len(f.Use))
	for _, use := range f.Use {
		// An absolute path names a module outside the repository.
		if !filepath.IsAbs(use.Path) {
			uses = append(uses, path.Join(path.Dir(file), filepath.ToSlash(use.Path)))
		}
	}

	return finding{pins: pins, warnings: warnings, uses: uses}, nil
}

func directivePins(file string, data []byte, goStmt *modfile.Go, toolchain *modfile.Toolchain, goKind, toolchainKind Kind) ([]Pin, []string) {
	var pins []Pin
	var warnings []string
	directive := func(line *modfile.Line, token string, kind Kind) {
		stmt := string(data[line.Start.Byte:line.End.Byte])
		v, span, ok := parseVersionAt(token, line.Start.Byte+strings.LastIndex(stmt, token))
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d: %q is not a release version; left unmanaged", file, line.Start.Line, token))

			return
		}
		pins = append(pins, Pin{File: file, Line: line.Start.Line, Kind: kind, Version: v, Span: span})
	}
	if goStmt != nil {
		directive(goStmt.Syntax, goStmt.Version, goKind)
	}
	// "toolchain default" names no version.
	if toolchain != nil && toolchain.Name != "default" {
		directive(toolchain.Syntax, toolchain.Name, toolchainKind)
	}

	return pins, warnings
}

// linePin builds a pin from the first capture group of re in l. It returns nothing when re
// does not match.
func linePin(file string, l textLine, re *regexp.Regexp, kind Kind) ([]Pin, []string) {
	m := re.FindStringSubmatchIndex(l.text)
	if m == nil {
		return nil, nil
	}
	raw := l.text[m[2]:m[3]]
	v, span, ok := parseVersionAt(raw, l.start+m[2])
	if !ok {
		return nil, []string{fmt.Sprintf("%s:%d: %q is not a release version; left unmanaged", file, l.no, raw)}
	}

	return []Pin{{File: file, Line: l.no, Kind: kind, Version: v, Span: span}}, nil
}
