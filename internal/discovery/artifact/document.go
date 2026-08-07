package artifact

import (
	"regexp"
	"strings"
)

// titleLinkPattern matches a top-level heading written as a single Markdown link, the form the
// discovery skill uses to point a document at the issue that prompted it.
var titleLinkPattern = regexp.MustCompile(`^#\s+\[[^\]]*\]\(([^)\s]+)\)\s*$`)

// TrackingURL reports the URL the discovery document links from its title. Discovery documents
// open with the issue that prompted the investigation, and publishing relates its epics back to
// that issue. A document whose title carries no link yields an empty string.
func TrackingURL(content []byte) string {
	// The title is the document's first content, the same rule Load validates. Reading further
	// would pick up links from prose that were never meant to identify the investigation.
	title, _, _ := strings.Cut(strings.TrimSpace(string(content)), "\n")
	match := titleLinkPattern.FindStringSubmatch(strings.TrimSpace(title))
	if match == nil {
		return ""
	}

	return match[1]
}
