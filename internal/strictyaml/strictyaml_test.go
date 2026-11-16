package strictyaml

import (
	"errors"
	"strings"
	"testing"
)

type config struct {
	Name string `yaml:"name"`
}

func TestDecode(t *testing.T) {
	for name, tc := range map[string]struct {
		data, want, err string
	}{
		"one document":               {data: "name: devex\n", want: "devex"},
		"empty":                      {data: "", err: "holds no YAML document"},
		"comments only":              {data: "# nothing yet\n", err: "holds no YAML document"},
		"leading separator":          {data: "---\nname: devex\n", want: "devex"},
		"unknown key":                {data: "nmae: devex\n", err: "field nmae not found"},
		"second document":            {data: "name: devex\n---\nname: other\n", err: "more than one YAML document"},
		"trailing separator":         {data: "name: devex\n---\n", want: "devex"},
		"trailing comments":          {data: "name: devex\n---\n# next\n---\n", want: "devex"},
		"third document":             {data: "name: devex\n---\n---\nname: other\n", err: "more than one YAML document"},
		"bare separator":             {data: "---\n", err: "holds no YAML document"},
		"explicit null":              {data: "~\n", err: "holds no YAML document"},
		"comment header":             {data: "---\n# header\n---\nname: devex\n", want: "devex"},
		"unknown key after a header": {data: "---\n# header\n---\nnmae: devex\n", err: "field nmae not found"},
		"bad second document":        {data: "name: devex\n---\n[unterminated\n", err: "did not find expected"},
	} {
		got := config{Name: "preset"}
		err := Decode([]byte(tc.data), &got)
		switch {
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: Decode() error = %v, want %q", name, err, tc.err)
		case tc.err == "" && (err != nil || got.Name != tc.want):
			t.Errorf("%s: Decode() = %q, %v; want %q", name, got.Name, err, tc.want)
		}
	}
}

func TestDecodeReportsEmptyDataAsErrEmpty(t *testing.T) {
	if err := Decode([]byte("# filled in later\n---\n"), &config{}); !errors.Is(err, ErrEmpty) {
		t.Errorf("Decode(comments and a separator) = %v, want ErrEmpty", err)
	}
}

func TestHasTopLevelKeyReadsTheDocumentDecodeWould(t *testing.T) {
	for data, want := range map[string]bool{
		"targets: {}\n":                     true,
		"---\n# header\n---\ntargets: {}\n": true,
		"release: {}\n":                     false,
		"release: targets\n":                false, // a value, not a key
		"- targets\n- other\n":              false, // a list, not a mapping
		"release: {}\n---\ntargets: {}\n":   false, // Decode rejects it, so it is not foreign
		"targets: {}\n---\ntargets: {}\n":   false,
		"":                                  false,
	} {
		if got := HasTopLevelKey([]byte(data), "targets"); got != want {
			t.Errorf("HasTopLevelKey(%q) = %t, want %t", data, got, want)
		}
	}
}
