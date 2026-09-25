package policy

import (
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Policy{"": Latest, "latest": Latest, "minor": Minor, "patch": Patch, "pin": Pin} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"hold", "major", "Latest"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}

func TestPickHonorsEachPolicy(t *testing.T) {
	candidates := []string{"v1.8.1", "v1.8.3", "v1.9.0", "v2.0.0", "v2.1.0-rc.1", "latest"}
	cases := []struct {
		policy  Policy
		current string
		want    string
	}{
		{Latest, "v1.8.2", "v2.0.0"},
		{Minor, "v1.8.2", "v1.9.0"},
		{Patch, "v1.8.2", "v1.8.3"},
		{Pin, "v1.8.2", ""},
		{Latest, "v2.0.0", ""},
		{Patch, "v1.8.3", ""},
		{Latest, "latest", ""},
	}
	for _, c := range cases {
		if got := c.policy.Pick(c.current, candidates); got != c.want {
			t.Errorf("%s.Pick(%q) = %q, want %q", c.policy, c.current, got, c.want)
		}
	}
}

func TestParseAllAndUnknown(t *testing.T) {
	if _, err := ParseAll("mise.policies", map[string]string{"b": "hold", "a": "nope"}); err == nil || !strings.HasPrefix(err.Error(), "mise.policies.a:") {
		t.Errorf("err = %v, want the first sorted key named", err)
	}
	policies, err := ParseAll("buf.policies", map[string]string{"x": "pin", "y": "", "z": "minor"})
	if err != nil || policies["y"] != Latest {
		t.Fatalf("ParseAll = %v, %v", policies, err)
	}
	if got := Unknown(policies, map[string]bool{"y": true}); !slices.Equal(got, []string{"x", "z"}) {
		t.Errorf("Unknown = %q, want [x z]", got)
	}
}

func TestPickComparesWithOrWithoutV(t *testing.T) {
	if got := Minor.Pick("26.8.2", []string{"26.10.0", "27.0.0", "26.9.1"}); got != "26.10.0" {
		t.Errorf("Minor.Pick = %q, want 26.10.0", got)
	}
}

func TestFormatKeepsStyle(t *testing.T) {
	cases := []struct{ like, v, want string }{
		{"v1.72.0", "1.73.0", "v1.73.0"},
		{"26.8", "26.10.0", "26.10"},
		{"2.13.2", "v2.14.0", "2.14.0"},
		{"4", "4.0.7", "4"},
	}
	for _, c := range cases {
		if got := Format(c.like, c.v); got != c.want {
			t.Errorf("Format(%q, %q) = %q, want %q", c.like, c.v, got, c.want)
		}
	}
}

func TestComponentsAndPrefix(t *testing.T) {
	if got := Components("v1.2.3"); got != 3 {
		t.Errorf("Components(v1.2.3) = %d, want 3", got)
	}
	if got := Components("lts"); got != 0 {
		t.Errorf("Components(lts) = %d, want 0", got)
	}
	if got := Prefix("v1.2.3", 2); got != "v1.2" {
		t.Errorf("Prefix = %q, want v1.2", got)
	}
}
