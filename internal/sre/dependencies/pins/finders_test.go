package pins

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// assertPins checks each pin's version and line, and that its span covers exactly the
// version text, which is what a rewrite replaces.
func assertPins(t *testing.T, data string, got []Pin, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d pins %v, want %d", len(got), got, len(want))
	}
	for i, p := range got {
		if loc := fmt.Sprintf("%s@%d", p.Version, p.Line); loc != want[i] {
			t.Errorf("pin %d = %s, want %s", i, loc, want[i])
		}
		if span := data[p.Span.Start:p.Span.End]; span != p.Version.String() {
			t.Errorf("pin %d span covers %q, want %q", i, span, p.Version.String())
		}
	}
}

func assertWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("no warning containing %q in %q", substr, warnings)
}

func TestFindMise(t *testing.T) {
	data := `[env]
go = "ignored outside tools"

[tools]

# Keep in step with go.mod.
go = "1.26.6"
jq = "latest"
`
	pins, warnings := findMise("mise.toml", []byte(data))
	assertPins(t, data, pins, "1.26.6@7")
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings %q", warnings)
	}
}

func TestFindMiseWarnsOnAliases(t *testing.T) {
	// devex manages only the canonical go key, so an alias pin is left to the user.
	for _, data := range []string{
		"[tools]\ngolang = \"1.26.6\"\n",
		"[tools]\n\"core:go\" = \"1.26.6\"\n",
		"[tools.golang]\nversion = \"1.26.6\"\n",
		"[tools]\n\"aqua:golang/go\" = \"1.26.6\"\n",
		"[tools]\n\"asdf:kennyp/asdf-golang\" = \"1.26.6\"\n",
		"[tools.\"aqua:golang/go\"]\nversion = \"1.26.6\"\n",
	} {
		pins, warnings := findMise("mise.toml", []byte(data))
		if len(pins) != 0 {
			t.Errorf("%q: pins = %+v, want none", data, pins)
		}
		assertWarning(t, warnings, "write it as go")
	}
}

func TestFindMiseInlineTableAndQuotedKey(t *testing.T) {
	data := "[tools]\n\"go\" = { version = \"1.26\", postinstall = \"echo\" }\n"
	pins, _ := findMise("mise.toml", []byte(data))
	assertPins(t, data, pins, "1.26@2")
}

func TestFindMiseWarnsOnUnmanagedForms(t *testing.T) {
	_, warnings := findMise("mise.toml", []byte("[tools]\ngo = \"latest\"\n"))
	assertWarning(t, warnings, `"latest" is not a release version`)

	_, warnings = findMise("mise.toml", []byte("[tools]\ngo = [\"1.26\", \"1.25\"]\n"))
	assertWarning(t, warnings, "devex cannot manage this go pin")

	// A version key inside a string or a longer key name is not the tool's own.
	data := "[tools]\ngo = { min-version = \"1.0\", postinstall = \"echo version = '9.9'\", version = \"1.26.6\" }\n"
	pins, _ := findMise("mise.toml", []byte(data))
	assertPins(t, data, pins, "1.26.6@2")

	_, warnings = findMise("mise.toml", []byte("[tools.go]\nversion = [\"1.25\", \"1.26\"]\n"))
	assertWarning(t, warnings, "devex cannot manage this go pin")

	// A Go tool whose name ends in -go is not Go itself.
	if _, warnings = findMise("mise.toml", []byte("[tools]\n\"go:connectrpc.com/connect/cmd/protoc-gen-connect-go\" = \"1.21.0\"\n")); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for protoc-gen-connect-go", warnings)
	}

	// A root-table key ending in .go that is not tools.go draws no warning.
	if _, warnings = findMise("mise.toml", []byte("tasks.go = \"go run .\"\n")); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for tasks.go", warnings)
	}

	// A version inside a nested inline table is not the tool's own.
	_, warnings = findMise("mise.toml", []byte("[tools]\ngo = { install_env = { version = \"x\" }, version = \"1.26.6\" }\n"))
	assertWarning(t, warnings, "devex cannot manage this go pin")
}

func TestIsMiseLock(t *testing.T) {
	for p, want := range map[string]bool{
		"mise.lock":                true,
		"mise.ci.lock":             true,
		".config/mise/config.lock": true,
		"svc/mise.local.lock":      true,
		"go.sum.lock":              false,
		"mise.toml":                false,
	} {
		if got := IsMiseLock(p); got != want {
			t.Errorf("IsMiseLock(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsMiseConfig(t *testing.T) {
	for p, want := range map[string]bool{
		"mise.toml":                   true,
		".mise.toml":                  true,
		"svc/mise.toml":               true,
		"mise.ci.toml":                true,
		".mise.local.toml":            true,
		".config/mise.toml":           true,
		".config/mise/config.toml":    true,
		".config/mise/conf.d/go.toml": true,
		"mise/config.toml":            true,
		".mise/config.toml":           true,
		"mise.ci.local.toml":          true,
		".mise/conf.d/go.toml":        true,
		"mise/config.ci.toml":         true,
		".mise/config.local.toml":     true,
		"config.toml":                 false,
		"mise.toml.bak":               false,
		"conf.d/go.toml":              false,
	} {
		if got := isMiseConfig(p); got != want {
			t.Errorf("isMiseConfig(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestFindGoVersionFile(t *testing.T) {
	data := "# pinned\ngo1.26.6\n"
	pins, _ := findGoVersionFile(".go-version", []byte(data))
	assertPins(t, data, pins, "1.26.6@2")
}

func TestFindToolVersions(t *testing.T) {
	data := "nodejs 22.1.0\ngolang 1.26.6\n"
	pins, _ := findToolVersions(".tool-versions", []byte(data))
	assertPins(t, data, pins, "1.26.6@2")

	data = "go 1.26\n"
	pins, _ = findToolVersions(".tool-versions", []byte(data))
	assertPins(t, data, pins, "1.26@1")
}

func TestFindGoMod(t *testing.T) {
	data := `module example.com/app

go 1.26.6 // floor

toolchain go1.27.1

require go.uber.org/zap v1.27.1
`
	f, err := findGoMod("go.mod", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	pins := f.pins
	assertPins(t, data, pins, "1.26.6@3", "1.27.1@5")
	if pins[0].Kind != KindGoDirective || pins[0].IsToolchain() {
		t.Errorf("go directive = %s, toolchain %v; want a non-toolchain floor", pins[0].Kind, pins[0].IsToolchain())
	}
	if pins[1].Kind != KindGoToolchain || !pins[1].IsToolchain() {
		t.Errorf("toolchain directive = %s, want a toolchain pin", pins[1].Kind)
	}
}

func TestFindGoModIgnoresDefaultToolchain(t *testing.T) {
	data := "module m\n\ngo 1.26.6\n\ntoolchain default\n"
	f, err := findGoMod("go.mod", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	pins := f.pins
	assertPins(t, data, pins, "1.26.6@3")
}

func TestFindGoModFailsOnUnparsableFile(t *testing.T) {
	if _, err := findGoMod("go.mod", []byte("module m\n\ngo 1.26.6\nbogus directive here\n")); err == nil {
		t.Error("an unparsable go.mod produced no error")
	}
}

func TestFindGoWork(t *testing.T) {
	data := "go 1.26.0\n\ntoolchain go1.26.6\n\nuse ./app\nuse /opt/shared\n"
	f, err := findGoWork("go.work", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	pins := f.pins
	assertPins(t, data, pins, "1.26.0@1", "1.26.6@3")
	if !slices.Equal(f.uses, []string{"app"}) {
		t.Errorf("uses = %q, want [app]", f.uses)
	}
	if pins[0].Kind != KindWorkGo || pins[0].IsToolchain() || !pins[1].IsToolchain() {
		t.Errorf("kinds = %s/%s, want a go.work floor and a toolchain pin", pins[0].Kind, pins[1].Kind)
	}
}

func TestParseVersionAtRefusesMalformedText(t *testing.T) {
	for _, raw := range []string{"01.22", "1.22.01", "x1.22", "1.22x"} {
		if _, _, ok := parseVersionAt(raw, 0); ok {
			t.Errorf("parseVersionAt(%q) accepted malformed text", raw)
		}
	}
	for _, raw := range []string{" go1.22.1 ", "1.22"} {
		if _, _, ok := parseVersionAt(raw, 0); !ok {
			t.Errorf("parseVersionAt(%q) refused a valid version", raw)
		}
	}
}

func TestFindMiseTableForms(t *testing.T) {
	data := "tools.go = \"1.26.6\"\n\n[tools.go]\nversion = \"1.26.6\"\npostinstall = \"echo\"\n\n[env]\nversion = \"9.9.9\"\n"
	pins, _ := findMise("mise.toml", []byte(data))
	assertPins(t, data, pins, "1.26.6@1", "1.26.6@4")
}

func TestFindSetupGoRefusesBlockScalar(t *testing.T) {
	data := "jobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: |\n            1.26.6\n"
	pins, warnings := findSetupGo(".github/workflows/ci.yaml", []byte(data))
	if len(pins) != 0 {
		t.Errorf("got pins %v from a block scalar", pins)
	}
	assertWarning(t, warnings, "block scalar")
}

func TestFindSetupGoWithAnchorAndTag(t *testing.T) {
	data := "steps:\n  - uses: actions/setup-go@v5\n    with:\n      go-version: &go \"1.26.6\"\n  - uses: actions/setup-go@v5\n    with:\n      go-version: !!str 1.26.6\n"
	pins, _ := findSetupGo(".github/workflows/ci.yaml", []byte(data))
	assertPins(t, data, pins, "1.26.6@4", "1.26.6@7")
}

func TestFindSetupGoAfterMultibyteText(t *testing.T) {
	data := "steps:\n  - uses: actions/setup-go@v5\n    with: { name: \"Téléchargement\", go-version: '1.26.6' }\n"
	pins, _ := findSetupGo(".github/workflows/ci.yaml", []byte(data))
	assertPins(t, data, pins, "1.26.6@3")
}

func TestFindSetupGo(t *testing.T) {
	data := `jobs:
  test:
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.6'
      - uses: actions/setup-go@v5
        with:
          go-version: 1.26.x
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: actions/checkout@v4
        with:
          go-version: '9.9.9'
`
	pins, warnings := findSetupGo(".github/workflows/ci.yaml", []byte(data))
	assertPins(t, data, pins, "1.26.6@6", "1.26@9")
	assertWarning(t, warnings, "comes from an expression")
}

func TestIsWorkflow(t *testing.T) {
	for p, want := range map[string]bool{
		".github/workflows/ci.yaml":        true,
		".github/workflows/ci.yml":         true,
		".github/actions/setup/action.yml": true,
		".github/workflows/nested/x.yaml":  false,
		"ci.yaml":                          false,
		".github/dependabot.yml":           false,
	} {
		if got := isWorkflow(p); got != want {
			t.Errorf("isWorkflow(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestCompileGlobRejectsUnsupportedPatterns(t *testing.T) {
	// Negation, an unclosed class, a class of only /, a reversed range, and a POSIX class.
	for _, pattern := range []string{"!docker/api/", "Dockerfile.[ab", "d/a[/]c", "d/a[z-a]c", "v[[:digit:]]"} {
		if _, err := compileGlob(pattern); err == nil {
			t.Errorf("compileGlob(%q) succeeded, want an error", pattern)
		}
	}
}

func TestCompileDeclaredRequiresVersionGroup(t *testing.T) {
	if _, err := CompileDeclared([]string{"x"}, `GO=(\S+)`); err == nil {
		t.Error("pattern without a version group compiled")
	}
	if _, err := CompileDeclared(nil, `GO=(?P<version>\S+)`); err == nil {
		t.Error("pin without files compiled")
	}
	if _, err := CompileDeclared([]string{"x"}, `(?P<version>[`); err == nil {
		t.Error("invalid regex compiled")
	}
}

func TestFindDeclaredKeepsGoPrefix(t *testing.T) {
	d, err := CompileDeclared([]string{"*.env"}, `GOTOOLCHAIN=(?P<version>go\S+)`)
	if err != nil {
		t.Fatal(err)
	}
	data := "GOTOOLCHAIN=go1.26.6\n"
	pins, _ := findDeclared("build.env", []byte(data), d)
	// The span covers the digits alone, so a rewrite leaves the go prefix in place.
	assertPins(t, data, pins, "1.26.6@1")
}

func TestFindDeclared(t *testing.T) {
	d, err := CompileDeclared([]string{"docker-compose*.yaml"}, `GO_IMAGE_TAG:\s*"?(?P<version>\d+\.\d+(?:\.\d+)?)`)
	if err != nil {
		t.Fatal(err)
	}
	data := "services:\n  api:\n    build:\n      args:\n        GO_IMAGE_TAG: \"1.26.6-alpine3.24\"\n"
	pins, _ := findDeclared("docker-compose.yaml", []byte(data), d)
	assertPins(t, data, pins, "1.26.6@5")
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"docker-compose*.yaml", "docker-compose.dev.yaml", true},
		{"docker-compose*.yaml", "deploy/docker-compose.yaml", true},
		{"**/testdata/**", "testdata/Dockerfile", true},
		{"**/testdata/**", "pkg/x/testdata/go.mod", true},
		{"**/testdata/**", "pkg/footestdata/go.mod", false},
		{"build/*.Dockerfile", "build/api.Dockerfile", true},
		{"build/*.Dockerfile", "build/x/api.Dockerfile", false},
		{"examples", "examples/go.mod", true},
		{"examples", "svc/examples/Dockerfile", true},
		{"examples/", "examples/Dockerfile", true},
		{"examples/", "examples", false},
		{"/legacy", "legacy/go.mod", true},
		{"/legacy", "svc/legacy/go.mod", false},
		{"vendor/**", "vendor/x/go.mod", true},
		{"vendor/**", "vendor", false},
		{"Dockerfile.[ab]", "Dockerfile.a", true},
		{"Dockerfile.[ab]", "Dockerfile.c", false},
		{"Dockerfile.[!ab]", "Dockerfile.c", true},
		{"Dockerfile.[^ab]", "Dockerfile.c", true},
		{"Dockerfile.[^ab]", "Dockerfile.a", false},
		{"d/a[!b]c", "d/axc/Dockerfile", true},
		{"d/a[!b]c", "d/a/c/Dockerfile", false},
		{"d/a[--0]c", "d/a.c/Dockerfile", true},
		{"d/a[--0]c", "d/a0c/Dockerfile", true},
		{"d/a[--0]c", "d/a/c/Dockerfile", false},
		{"café/", "café/go.mod", true},
		{`caf\é/`, "café/go.mod", true},
		{"x[]a]", "x]", true},
		{`x[\[:]`, "x:", true},
		{"/src/a**b", "src/axyb/go.mod", true},
		{"/src/a**b", "src/a/x/b/go.mod", false},
		{"x[]a]", "xa", true},
		{"x[!]]", "x]", false},
		{"x[!]]", "xb", true},
		{`x[\]]`, "x]", true},
		{`build\*`, "build*", true},
		{`build\*`, "buildx", false},
	}
	for _, c := range cases {
		g, err := compileGlob(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := g.Match(c.path); got != c.want {
			t.Errorf("Glob(%q).Match(%q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestFindMiseTracksStringsAndComments(t *testing.T) {
	cases := map[string]string{
		"a task script holding a fake [tools]":          "[tasks.x]\nrun = \"\"\"\n[tools]\ngo = \"1.25.0\"\n\"\"\"\n[tools]\ngo = \"1.26.6\"\n",
		"a multi-line inline table":                     "[tools]\nnode = {\n  go = \"1.25.0\"\n}\ngo = \"1.26.6\"\n",
		"an array row that looks like a table header":   "[tools]\nplatforms = [\n  [\"linux\", \"amd64\"],\n  [\"darwin\"]\n]\ngo = \"1.26.6\"\n",
		"a triple quote in a comment":                   "# wrap task scripts in \"\"\"\n[tools]\ngo = \"1.26.6\"\n",
		"a script on its own array line":                "[tasks.x]\nrun = [\n  \"\"\"\ntools.go = \"1.25.0\"\n\"\"\",\n]\n[tools]\ngo = \"1.26.6\"\n",
		"one line closing a script and opening another": "[tasks.x]\nrun = [\"\"\"\necho\n\"\"\", \"\"\"\n[tools]\ngo = \"1.20\"\n\"\"\"]\n[tools]\ngo = \"1.26.6\"\n",
		"a # inside a string before an opener":          "tasks.x = { description = \"step #1\", run = \"\"\"\n[tools]\ngo = \"1.20\"\n\"\"\" }\n[tools]\ngo = \"1.26.6\"\n",
		"a triple quote inside an ordinary string":      "description = \"use ,''' here\"\n[tools]\ngo = \"1.26.6\"\n",
	}
	for name, data := range cases {
		pins, warnings := findMise("mise.toml", []byte(data))
		if len(pins) != 1 || pins[0].Version.String() != "1.26.6" || len(warnings) != 0 {
			t.Errorf("%s: pins = %+v, warnings = %q; want only the real 1.26.6 pin", name, pins, warnings)
		}
	}
}

func TestFindMiseInlineAndDottedVersionForms(t *testing.T) {
	data := "tools.go = { version = \"1.26.6\" }\n\n[tools]\ngo.version = \"1.26.6\"\n"
	pins, _ := findMise("mise.toml", []byte(data))
	assertPins(t, data, pins, "1.26.6@1", "1.26.6@4")
}

func TestFindMiseIgnoresOtherGoKeys(t *testing.T) {
	data := "[tools]\ngo.version = \"1.26.6\"\ngo.os = [\"linux\"]\n"
	pins, warnings := findMise("mise.toml", []byte(data))
	assertPins(t, data, pins, "1.26.6@2")
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for go.os", warnings)
	}
}

func TestFindMiseWarnsOnUnmanagedRootTable(t *testing.T) {
	_, warnings := findMise("mise.toml", []byte("tools = { go = \"1.26.6\" }\n"))
	assertWarning(t, warnings, "devex cannot manage this go pin")
}
