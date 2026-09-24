package pins

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func findDocker(t *testing.T, data string, images ...string) ([]Pin, []string) {
	t.Helper()
	if len(images) == 0 {
		images = DefaultImages
	}

	return findDockerfile("Dockerfile", []byte(data), images)
}

func assertImages(t *testing.T, p Pin, refs ...string) {
	t.Helper()
	if len(p.Images) != len(refs) {
		t.Fatalf("pin has %d images, want %d", len(p.Images), len(refs))
	}
	for i, img := range p.Images {
		if got := img.Ref(p.Version); got != refs[i] {
			t.Errorf("image %d = %s, want %s", i, got, refs[i])
		}
	}
}

func TestDockerfileArgFedTag(t *testing.T) {
	data := `# syntax=docker/dockerfile:1.3

# Keep in step with go.mod.
ARG GO_IMAGE_TAG=1.26.6-alpine3.24
FROM --platform=$BUILDPLATFORM golang:${GO_IMAGE_TAG} as base
RUN go build ./...
FROM alpine:3.24
COPY --from=base /app /app
`
	pins, warnings := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@4")
	assertImages(t, pins[0], "golang:1.26.6-alpine3.24")
	if pins[0].Arg != "GO_IMAGE_TAG" {
		t.Errorf("Arg = %q, want GO_IMAGE_TAG", pins[0].Arg)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings %q", warnings)
	}
}

func TestDockerfileLiteralTagWithRegistryAndDigest(t *testing.T) {
	data := "FROM public.ecr.aws/docker/library/golang:1.26.6-bookworm@sha256:abc123 AS build\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@1")
	img := pins[0].Images[0]
	if img.Repository != "public.ecr.aws/docker/library/golang" || img.TagSuffix != "-bookworm" {
		t.Errorf("image = %+v", img)
	}
	if img.Digest != "sha256:abc123" || data[img.DigestSpan.Start:img.DigestSpan.End] != "sha256:abc123" {
		t.Errorf("digest %q at span %q", img.Digest, data[img.DigestSpan.Start:img.DigestSpan.End])
	}
}

func TestDockerfileWarnsOnTaglessImage(t *testing.T) {
	for _, data := range []string{"FROM golang\n", "FROM golang@sha256:abc\n"} {
		pins, warnings := findDocker(t, data)
		if len(pins) != 0 {
			t.Errorf("%q: pins = %+v, want none", data, pins)
		}
		assertWarning(t, warnings, "has no tag")
	}
}

func TestDockerfileIgnoresStageReferences(t *testing.T) {
	data := "FROM golang:1.26.6 AS golang\nFROM golang AS test\n"
	pins, warnings := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@1")
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a stage reference", warnings)
	}
}

func TestDockerfileWarnsOnArgFedImageReference(t *testing.T) {
	pins, warnings := findDocker(t, "ARG GO_IMAGE=golang:1.26.6-alpine3.24\nFROM ${GO_IMAGE} AS build\n")
	if len(pins) != 0 {
		t.Errorf("pins = %+v, want none", pins)
	}
	assertWarning(t, warnings, "comes from a build arg")
}

func TestDockerfileLeavesArgFedDigestUnmanaged(t *testing.T) {
	pins, warnings := findDocker(t, "ARG GO_DIGEST=sha256:abc\nFROM golang:1.26.6@${GO_DIGEST}\n")
	if len(pins) != 0 {
		t.Errorf("pins = %+v, want none", pins)
	}
	assertWarning(t, warnings, "comes from a build arg")
}

func TestDockerfileDigestLineForArgFedPin(t *testing.T) {
	data := "ARG GO_VERSION=1.26.6\nFROM golang:${GO_VERSION}@sha256:abc\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@1")
	if img := pins[0].Images[0]; img.DigestLine != 2 {
		t.Errorf("DigestLine = %d, want the FROM line 2", img.DigestLine)
	}
}

func TestDockerfileContinuationEscapeAndLowercase(t *testing.T) {
	data := "# escape=`\nfrom --platform=linux/amd64 `\n  golang:1.26 as b\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26@3")
}

func TestDockerfileDefaultInsideFrom(t *testing.T) {
	data := "ARG GO_VERSION\nFROM golang:${GO_VERSION:-1.26.6}-alpine\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@2")
	assertImages(t, pins[0], "golang:1.26.6-alpine")
}

func TestDockerfileBareVariableWithFromSuffix(t *testing.T) {
	data := "ARG GO_VERSION=1.26.6\nFROM golang:$GO_VERSION-alpine3.24\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@1")
	assertImages(t, pins[0], "golang:1.26.6-alpine3.24")
}

func TestDockerfileQuotedArg(t *testing.T) {
	data := "ARG GO_VERSION=\"1.26.6\"\nFROM golang:${GO_VERSION}\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@1")
}

func TestDockerfileOneArgFeedsTwoStages(t *testing.T) {
	data := "ARG GO=1.26.6\nFROM golang:${GO} AS build\nFROM golang:${GO}-alpine AS test\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@1")
	assertImages(t, pins[0], "golang:1.26.6", "golang:1.26.6-alpine")
}

func TestDockerfileEmptyArgDefaultFallsBackToFromDefault(t *testing.T) {
	data := "ARG GO_VERSION=\nFROM golang:${GO_VERSION:-1.26.6}-alpine\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@2")
}

func TestDockerfileExpandsRegistryFromArg(t *testing.T) {
	data := "ARG REGISTRY=public.ecr.aws/docker/library\nFROM ${REGISTRY}/golang:1.26.6\n"
	pins, _ := findDocker(t, data)
	assertPins(t, data, pins, "1.26.6@2")
	if img := pins[0].Images[0]; img.Repository != "public.ecr.aws/docker/library/golang" || img.Unresolved {
		t.Errorf("image = %+v, want the ARG default expanded", img)
	}
}

func TestDockerfileKeepsUnresolvableRegistry(t *testing.T) {
	pins, _ := findDocker(t, "FROM ${ECR}/docker/library/golang:1.26.6\n")
	if len(pins) != 1 || !pins[0].Images[0].Unresolved {
		t.Fatalf("pins = %+v, want the pin kept and its registry marked unresolved", pins)
	}
}

func TestMatchImage(t *testing.T) {
	cases := []struct {
		pattern, repo string
		want          bool
	}{
		{"golang", "golang", true},
		{"golang", "public.ecr.aws/docker/library/golang", true},
		{"golang", "golang/tools", false},
		{"golang", "ghcr.io/org/golang-tools", false},
		{"cgr.dev/chainguard/go", "cgr.dev/chainguard/go", true},
		{"cgr.dev/chainguard/go", "go", false},
	}
	for _, c := range cases {
		if got := matchImage([]string{c.pattern}, c.repo); got != c.want {
			t.Errorf("matchImage(%q, %q) = %v, want %v", c.pattern, c.repo, got, c.want)
		}
	}
}

func TestDockerfileArgWithoutDefaultWarns(t *testing.T) {
	pins, warnings := findDocker(t, "ARG GO_IMAGE_TAG\nFROM golang:${GO_IMAGE_TAG}\n")
	if len(pins) != 0 {
		t.Errorf("got pins %v, want none", pins)
	}
	assertWarning(t, warnings, "has no default")
}

func TestDockerfileArgAfterFirstFromIsInvisibleToFrom(t *testing.T) {
	_, warnings := findDocker(t, "FROM alpine\nARG GO=1.26.6\nFROM golang:${GO}\n")
	assertWarning(t, warnings, "not an ARG declared before the first FROM")
}

func TestDockerfileUnmanagedTags(t *testing.T) {
	for _, tag := range []string{"latest", "alpine", "1", "1.27rc1-alpine"} {
		pins, warnings := findDocker(t, "FROM golang:"+tag+"\n")
		if len(pins) != 0 {
			t.Errorf("tag %q produced pins %v", tag, pins)
		}
		assertWarning(t, warnings, "has no release version")
	}
}

func TestDockerfileIgnoresOtherImagesAndStageRefs(t *testing.T) {
	pins, warnings := findDocker(t, "FROM node:22.1 AS web\nFROM web\nFROM ghcr.io/org/golang-tools:1.0.0\n")
	if len(pins) != 0 || len(warnings) != 0 {
		t.Errorf("got pins %v warnings %q, want none", pins, warnings)
	}
}

func TestDockerfileCustomImage(t *testing.T) {
	data := "FROM cgr.dev/chainguard/go:1.26.6\n"
	pins, _ := findDocker(t, data, "cgr.dev/chainguard/go")
	assertPins(t, data, pins, "1.26.6@1")
}

func TestIsDockerfile(t *testing.T) {
	for p, want := range map[string]bool{
		"Dockerfile":              true,
		"docker/api/Dockerfile":   true,
		"Dockerfile.dev":          true,
		"build/api.Dockerfile":    true,
		"Containerfile":           true,
		"dockerfile.md":           false,
		"pkg/dockerfile.go":       false,
		"Dockerfile.sh":           false,
		"infra/dockerfile.tf":     false,
		"Dockerfile.dockerignore": false,
		"dockerfile.prod":         true,
		"docs/dockerfiles.md":     false,
	} {
		if got := isDockerfile(p); got != want {
			t.Errorf("isDockerfile(%q) = %v, want %v", p, got, want)
		}
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	root := t.TempDir()
	if err := os.CopyFS(root, fsys); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestDiscover(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":                    "module app\n\ngo 1.26.6\n",
		"tools/go.mod":              "module tools\n\ngo 1.26.0\n",
		"mise.toml":                 "[tools]\n# GO_IMAGE_TAG must match\ngo = \"1.26.6\"\n",
		"docker/api/Dockerfile":     "ARG GO_IMAGE_TAG=1.26.6-alpine3.24\nFROM golang:${GO_IMAGE_TAG}\n",
		"docker/worker/Dockerfile":  "ARG GO_IMAGE_TAG=1.26.6-alpine3.24\nFROM golang:${GO_IMAGE_TAG}\n",
		"docker-compose.yaml":       "services:\n  api:\n    build:\n      args:\n        GO_IMAGE_TAG: 1.26.6-alpine3.24\n",
		"testdata/go.mod":           "module fixture\n\ngo 1.20\n",
		"pkg/x/testdata/Dockerfile": "FROM golang:1.19\n",
		".github/workflows/ci.yaml": "jobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.26.6'\n",
		"README.md":                 "Run with GO_IMAGE_TAG unset.\n",
	})

	res, err := Discover(Options{Root: root, ListFiles: WalkFiles})
	if err != nil {
		t.Fatal(err)
	}
	shadows := ShadowWarnings(root, res)

	var locs []string
	for _, p := range res.Pins {
		locs = append(locs, p.Location())
	}
	want := []string{".github/workflows/ci.yaml:6", "docker/api/Dockerfile:1", "docker/worker/Dockerfile:1", "go.mod:3", "mise.toml:3", "tools/go.mod:3"}
	if len(locs) != len(want) {
		t.Fatalf("pins at %v, want %v", locs, want)
	}
	for i := range want {
		if locs[i] != want[i] {
			t.Errorf("pin %d at %s, want %s", i, locs[i], want[i])
		}
	}
	if len(res.Modules) != 2 {
		t.Errorf("modules = %v, want . and tools", res.Modules)
	}

	// The compose build-arg can override the ARG. The mise comment, the README, and the
	// sibling Dockerfile cannot.
	assertWarning(t, shadows, "GO_IMAGE_TAG also appears in docker-compose.yaml:5")
	for _, w := range shadows {
		for _, noisy := range []string{"appears in mise.toml", "appears in README.md", "appears in docker/"} {
			if strings.Contains(w, noisy) {
				t.Errorf("unexpected shadow warning %q", w)
			}
		}
	}
}

func TestDiscoverDeclaredPinsAndExcludes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":              "module app\n\ngo 1.26.6\n",
		"docker-compose.yaml": "        GO_IMAGE_TAG: 1.26.6-alpine3.24\n",
		"legacy/go.mod":       "module legacy\n\ngo 1.21\n",
	})
	d, err := CompileDeclared([]string{"docker-compose*.yaml"}, `GO_IMAGE_TAG:\s*(?P<version>\d+\.\d+\.\d+)`)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Discover(Options{Root: root, ListFiles: WalkFiles, Declared: []Declared{d}, Exclude: []string{"legacy/**"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pins) != 2 || res.Pins[0].Kind != KindDeclared || res.Pins[1].File != "go.mod" {
		t.Errorf("pins = %+v, want the declared compose pin and root go.mod", res.Pins)
	}
}

func TestDiscoverSkipsListedFilesMissingFromDisk(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": "module app\n\ngo 1.26.6\n"})
	list := func(string) ([]string, error) { return []string{"go.mod", "deleted/Dockerfile"}, nil }

	if _, err := Discover(Options{Root: root, ListFiles: list}); err != nil {
		t.Fatalf("Discover failed on a deleted tracked file: %v", err)
	}
}
