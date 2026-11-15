package translate

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/project-init/devex/internal/localize/catalog"
	"golang.org/x/sync/errgroup"
)

// message is one source message to translate.
type message struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// model translates messages from the base locale into locale, following rubric. It returns a
// translation per message id; ids it leaves out count as rejected.
type model interface {
	Translate(ctx context.Context, locale, rubric string, messages []message) (map[string]string, error)
}

type options struct {
	dryRun      bool // propose translations without writing catalogs
	batchSize   int
	concurrency int
	rubric      string
}

type proposal struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Translation string `json:"translation"`
}

type rejection struct {
	ID          string `json:"id"`
	Translation string `json:"translation"`
	Reason      string `json:"reason"`
}

// result is what a run did for one locale.
type result struct {
	Locale     string      `json:"locale"`
	Translated []proposal  `json:"translated"`
	Rejected   []rejection `json:"rejected"`
	Skipped    []string    `json:"skipped"`
}

// localePlan is one locale's catalog and the messages it lacks.
type localePlan struct {
	locale  string
	path    string
	work    []source
	skipped []string
}

type source struct {
	id   catalog.ID
	text string
}

// errNoAPIKey is returned when translation needs Gemini and GEMINI_API_KEY is unset.
var errNoAPIKey = errors.New("set GEMINI_API_KEY to a Gemini API key to translate; create one at https://aistudio.google.com/apikey")

// plan reads the catalogs under localesDir and returns each selected locale's missing or empty
// messages, sorted by locale, without calling a model. A base-locale message that is a plural
// or select is skipped, since only its string form can be translated.
func plan(localesDir string, locales []string) ([]localePlan, error) {
	dirs, err := catalog.LocaleDirs(localesDir)
	if err != nil {
		return nil, err
	}
	byLocale := make(map[string]string, len(dirs))
	for _, dir := range dirs {
		byLocale[filepath.Base(dir)] = dir
	}
	baseDir, ok := byLocale[catalog.BaseLocale]
	if !ok {
		return nil, fmt.Errorf("no %s locale in %s", catalog.BaseLocale, localesDir)
	}
	for _, locale := range locales {
		if _, ok := byLocale[locale]; !ok {
			return nil, fmt.Errorf("unknown locale %q; available: %v", locale, slices.Sorted(maps.Keys(byLocale)))
		}
	}
	base, err := catalog.Load(filepath.Join(baseDir, catalog.MessagesFile))
	if err != nil {
		return nil, err
	}

	var plans []localePlan
	for _, locale := range slices.Sorted(maps.Keys(byLocale)) {
		if len(locales) > 0 && !slices.Contains(locales, locale) {
			continue
		}
		path := filepath.Join(byLocale[locale], catalog.MessagesFile)
		target, err := catalog.Load(path)
		if errors.Is(err, os.ErrNotExist) && len(locales) == 0 {
			continue // a helper directory beside the locales, as audit skips
		}
		if err != nil {
			return nil, err
		}
		// The first entry for an id wins, as it does when the document writes back.
		existing := make(map[catalog.ID]catalog.Message, len(target.Messages))
		for _, msg := range target.Messages {
			if _, seen := existing[msg.ID]; !seen {
				existing[msg.ID] = msg
			}
		}
		lp := localePlan{locale: locale, path: path}
		for _, msg := range base.Messages {
			if current, ok := existing[msg.ID]; ok && !current.Untranslated() {
				continue
			}
			text, ok := sourceText(msg)
			if !ok {
				lp.skipped = append(lp.skipped, string(msg.ID))
				continue
			}
			lp.work = append(lp.work, source{id: msg.ID, text: text})
		}
		plans = append(plans, lp)
	}
	return plans, nil
}

// sourceText is the English text of a base-locale message: its message, its translation, or
// its id. A plural or select translation has no single text.
func sourceText(msg catalog.Message) (string, bool) {
	text, isText := msg.Text()
	if msg.Translation != nil && !isText {
		return "", false
	}
	for _, candidate := range []string{msg.Message, text, string(msg.ID)} {
		if candidate != "" {
			return candidate, true
		}
	}
	return "", false
}

// workCount is the number of messages the plans ask a model to translate.
func workCount(plans []localePlan) int {
	count := 0
	for _, lp := range plans {
		if lp.locale != catalog.BaseLocale {
			count += len(lp.work)
		}
	}
	return count
}

// localeRun collects one locale's batch results as they finish.
type localeRun struct {
	plan      localePlan
	batches   [][]proposal // per batch, so proposals keep source order whatever finishes first
	rejected  [][]rejection
	remaining int
	writeErr  error
}

// run translates every plan with m, writing each locale's accepted translations back as soon as
// its last batch finishes, unless dryRun is set. Batches from every locale share one pool of
// at most opts.concurrency requests. A failed request rejects its batch instead of stopping the
// run, so finished locales still get written and reported. The base locale needs no model: its
// missing translations take the source text. progress receives the number of messages each
// finished batch covered.
func run(ctx context.Context, localesDir string, plans []localePlan, m model, opts options, progress func(int)) ([]result, error) {
	baseDoc, err := catalog.LoadDocument(filepath.Join(localesDir, catalog.BaseLocale, catalog.MessagesFile))
	if err != nil {
		return nil, err
	}
	runs := make([]*localeRun, len(plans))
	var mu sync.Mutex // guards every localeRun
	var group errgroup.Group
	group.SetLimit(opts.concurrency)

	finish := func(lr *localeRun) {
		proposals := slices.Concat(lr.batches...)
		if opts.dryRun || len(proposals) == 0 {
			return
		}
		lr.writeErr = write(lr.plan.path, proposals, baseDoc)
	}

	for index, lp := range plans {
		batchCount := (len(lp.work) + opts.batchSize - 1) / opts.batchSize
		lr := &localeRun{plan: lp, batches: make([][]proposal, batchCount), rejected: make([][]rejection, batchCount), remaining: batchCount}
		runs[index] = lr
		if lp.locale == catalog.BaseLocale {
			lr.batches = [][]proposal{identity(lp.work)}
			finish(lr)
			continue
		}
		for batch := range batchCount {
			work := lp.work[batch*opts.batchSize : min((batch+1)*opts.batchSize, len(lp.work))]
			group.Go(func() error {
				proposals, rejections := translateBatch(ctx, m, lp.locale, opts.rubric, work)
				if progress != nil {
					progress(len(work))
				}
				mu.Lock()
				lr.batches[batch], lr.rejected[batch] = proposals, rejections
				lr.remaining--
				done := lr.remaining == 0
				mu.Unlock()
				if done {
					finish(lr)
				}
				return nil
			})
		}
	}
	_ = group.Wait()

	// Report every locale even after Ctrl-C, so the user learns which catalogs changed.
	results := make([]result, len(runs))
	errs := []error{ctx.Err()}
	for index, lr := range runs {
		r := result{
			Locale:     lr.plan.locale,
			Translated: append([]proposal{}, slices.Concat(lr.batches...)...),
			Rejected:   append([]rejection{}, slices.Concat(lr.rejected...)...),
			Skipped:    append([]string{}, lr.plan.skipped...),
		}
		if lr.writeErr != nil {
			// Nothing reached the catalog, so its translations count as rejected.
			for _, p := range r.Translated {
				r.Rejected = append(r.Rejected, rejection{ID: p.ID, Translation: p.Translation, Reason: "write failed: " + lr.writeErr.Error()})
			}
			r.Translated = []proposal{}
			errs = append(errs, fmt.Errorf("write %s: %w", lr.plan.locale, lr.writeErr))
		}
		results[index] = r
	}
	return results, errors.Join(errs...)
}

// identity proposes each base-locale message's source text as its translation.
func identity(work []source) []proposal {
	proposals := make([]proposal, len(work))
	for index, w := range work {
		proposals[index] = proposal{ID: string(w.id), Source: w.text, Translation: w.text}
	}
	return proposals
}

// translateBatch asks m for one batch. A failed request, such as a rate limit or a response
// the model cut short, rejects every message in the batch with the error as the reason.
func translateBatch(ctx context.Context, m model, locale, rubric string, work []source) ([]proposal, []rejection) {
	messages := make([]message, len(work))
	for index, w := range work {
		messages[index] = message{ID: string(w.id), Text: w.text}
	}
	translations, err := m.Translate(ctx, locale, rubric, messages)
	if err != nil {
		rejections := make([]rejection, len(work))
		for index, w := range work {
			rejections[index] = rejection{ID: string(w.id), Reason: err.Error()}
		}
		return nil, rejections
	}
	var proposals []proposal
	var rejections []rejection
	for _, w := range work {
		translation, ok := translations[string(w.id)]
		if reason := validate(w.text, translation, ok); reason != "" {
			rejections = append(rejections, rejection{ID: string(w.id), Translation: translation, Reason: reason})
			continue
		}
		proposals = append(proposals, proposal{ID: string(w.id), Source: w.text, Translation: translation})
	}
	return proposals, rejections
}

// validate returns why translation is unacceptable for source, or "" when it is acceptable.
func validate(source, translation string, returned bool) string {
	switch {
	case !returned:
		return "the model returned no translation"
	case translation == "":
		return "the translation is empty"
	case !samePlaceholders(source, translation):
		return fmt.Sprintf("placeholders differ: source has %v, translation has %v",
			catalog.Placeholders(source), catalog.Placeholders(translation))
	}
	return ""
}

// samePlaceholders reports whether a and b use the same placeholders the same number of times,
// in any order.
func samePlaceholders(a, b string) bool {
	left, right := catalog.Placeholders(a), catalog.Placeholders(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func write(path string, proposals []proposal, baseDoc *catalog.Document) error {
	doc, err := catalog.LoadDocument(path)
	if err != nil {
		return err
	}
	for _, p := range proposals {
		found, err := doc.SetTranslation(catalog.ID(p.ID), p.Translation)
		if err != nil {
			return err
		}
		if found {
			continue
		}
		if err := doc.AppendFrom(baseDoc, catalog.ID(p.ID), p.Translation); err != nil {
			return err
		}
	}
	return doc.Save(path)
}
