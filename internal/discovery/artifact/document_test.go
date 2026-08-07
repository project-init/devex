package artifact

import "testing"

func TestTrackingURL(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "linked title",
			content: "# [Replace Prettier with oxfmt](https://jira.test/browse/INIT-794)\n\nBody.\n",
			want:    "https://jira.test/browse/INIT-794",
		},
		{
			name:    "leading blank lines",
			content: "\n\n# [Audit logs](https://jira.test/browse/INIT-1)\n",
			want:    "https://jira.test/browse/INIT-1",
		},
		{
			name:    "plain title",
			content: "# Audit logs\n\nBody linking [an issue](https://jira.test/browse/INIT-2).\n",
		},
		{
			// Only the title identifies the investigation. A link further down is prose.
			name:    "link below the title",
			content: "# Audit logs\n\n[Tracking](https://jira.test/browse/INIT-3)\n",
		},
		{
			name:    "title with trailing prose",
			content: "# [Audit logs](https://jira.test/browse/INIT-4) and more\n",
		},
		{name: "empty document"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TrackingURL([]byte(test.content)); got != test.want {
				t.Fatalf("TrackingURL() = %q, want %q", got, test.want)
			}
		})
	}
}
