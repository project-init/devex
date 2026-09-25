package pins

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Glob matches slash-separated paths the way .gitignore does. A pattern without a slash
// matches a name at any depth; one with a slash matches from the repository root, where "**"
// spans directories. A pattern that matches a directory matches everything under it, and a
// trailing slash matches directories only.
type Glob struct {
	anyDepth bool
	dirOnly  bool
	re       *regexp.Regexp
}

func compileGlob(pattern string) (Glob, error) {
	if strings.HasPrefix(pattern, "!") {
		return Glob{}, fmt.Errorf("glob %q: negation is unsupported; list only the paths to match", pattern)
	}
	trimmed := strings.TrimSuffix(pattern, "/")
	expr, err := globToRegexp(strings.TrimPrefix(trimmed, "/"))
	if err != nil {
		return Glob{}, fmt.Errorf("glob %q: %w", pattern, err)
	}
	re, err := regexp.Compile("^" + expr + "$")
	if err != nil {
		return Glob{}, fmt.Errorf("glob %q: %w", pattern, err)
	}

	return Glob{anyDepth: !strings.Contains(trimmed, "/"), dirOnly: trimmed != pattern, re: re}, nil
}

// Match reports whether p, or any directory containing it, matches the glob.
func (g Glob) Match(p string) bool {
	for start := 0; ; {
		end := strings.IndexByte(p[start:], '/')
		last := end < 0
		if last {
			end = len(p)
		} else {
			end += start
		}
		// A directory-only pattern never names the file itself.
		if !last || !g.dirOnly {
			candidate := p[:end]
			if g.anyDepth {
				candidate = p[start:end]
			}
			if g.re.MatchString(candidate) {
				return true
			}
		}
		if last {
			return false
		}
		start = end + 1
	}
}

func globToRegexp(pattern string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		// As in .gitignore, ** spans directories only as a whole segment; elsewhere it is *.
		case strings.HasPrefix(pattern[i:], "**/") && segmentStart(pattern, i):
			b.WriteString("(?:.*/)?")
			i += 2
		case pattern[i:] == "**" && segmentStart(pattern, i):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		// QuoteMeta passes non-ASCII bytes through, so a multibyte rune survives byte by byte.
		case c == '\\' && i+1 < len(pattern):
			i++
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		case c == '[':
			end := classEnd(pattern, i)
			if end < 0 {
				return "", fmt.Errorf("unclosed [ at offset %d", i)
			}
			class, err := globClass(pattern[i+1 : end])
			if err != nil {
				return "", fmt.Errorf("character class at offset %d: %w", i, err)
			}
			b.WriteString(class)
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}

	return b.String(), nil
}

func segmentStart(pattern string, i int) bool {
	return i == 0 || pattern[i-1] == '/'
}

// classEnd returns the offset of the ] closing the class that opens at pattern[open], or
// -1. As in git, a ] first in the class or after a backslash is a member.
func classEnd(pattern string, open int) int {
	i := open + 1
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for ; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case ']':
			return i
		}
	}

	return -1
}

// globClass translates the body of a glob character class to an RE2 class. As in
// .gitignore, a class never matches the directory separator.
func globClass(body string) (string, error) {
	// git accepts both [!...] and [^...] as negation.
	negated := strings.HasPrefix(body, "!") || strings.HasPrefix(body, "^")
	if negated {
		body = body[1:]
	}
	class, err := classWithoutSeparator(body)
	switch {
	case err != nil:
		return "", err
	case negated:
		return "[^/" + class + "]", nil
	case class == "":
		return "", errors.New("matches only /")
	default:
		return "[" + class + "]", nil
	}
}

// classWithoutSeparator rewrites a glob character class body as an RE2 one without '/',
// splitting any range that spans it.
func classWithoutSeparator(body string) (string, error) {
	var b strings.Builder
	runes := []rune(body)
	// member reads the class member at runes[i], unescaping it, and returns the next offset.
	member := func(i int) (rune, int) {
		if runes[i] == '\\' && i+1 < len(runes) {
			return runes[i+1], i + 2
		}

		return runes[i], i + 1
	}
	for i := 0; i < len(runes); {
		// member consumes escapes whole, so runes[i] is never escaped here.
		if runes[i] == '[' && i+1 < len(runes) && runes[i+1] == ':' {
			return "", errors.New("POSIX classes such as [:digit:] are unsupported")
		}
		lo, next := member(i)
		hi := lo
		if next+1 < len(runes) && runes[next] == '-' {
			hi, next = member(next + 1)
		}
		i = next
		if hi < lo {
			return "", fmt.Errorf("range %c-%c runs backwards", lo, hi)
		}
		writeClassRange(&b, lo, min(hi, '/'-1))
		writeClassRange(&b, max(lo, '/'+1), hi)
	}

	return b.String(), nil
}

func writeClassRange(b *strings.Builder, lo, hi rune) {
	if lo > hi {
		return
	}
	b.WriteString(classMember(lo))
	if hi > lo {
		b.WriteString("-" + classMember(hi))
	}
}

// classMember escapes r for use inside an RE2 character class.
func classMember(r rune) string {
	if strings.ContainsRune(`\]-^[`, r) {
		return `\` + string(r)
	}

	return string(r)
}

func compileGlobs(patterns []string) ([]Glob, error) {
	globs := make([]Glob, 0, len(patterns))
	for _, p := range patterns {
		g, err := compileGlob(p)
		if err != nil {
			return nil, err
		}
		globs = append(globs, g)
	}

	return globs, nil
}

// Matcher returns a function reporting whether a slash-separated path matches any of patterns,
// in the syntax Options.Exclude takes.
func Matcher(patterns []string) (func(string) bool, error) {
	globs, err := compileGlobs(patterns)
	if err != nil {
		return nil, err
	}

	return func(p string) bool { return matchAny(globs, p) }, nil
}

func matchAny(globs []Glob, p string) bool {
	for _, g := range globs {
		if g.Match(p) {
			return true
		}
	}

	return false
}
