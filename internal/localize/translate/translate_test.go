package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/project-init/devex/internal/localize/catalog"
	"github.com/project-init/devex/internal/localize/config"
	"github.com/project-init/devex/internal/ui"
)

// fakeModel answers from a fixed table and records every batch it receives.
type fakeModel struct {
	mu      sync.Mutex
	answers map[string]map[string]string // locale -> id -> translation
	batches [][]message
}

func (f *fakeModel) Translate(_ context.Context, locale, _ string, messages []message) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, messages)
	result := make(map[string]string)
	for _, message := range messages {
		if translation, ok := f.answers[locale][message.ID]; ok {
			result[message.ID] = translation
		}
	}
	return result, nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupLocales writes an en-US source with four messages and an es-US catalog that translates
// one, leaves one empty, lacks one, and has a plural source to skip.
func setupLocales(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", catalog.MessagesFile), `{
  "language": "en-US",
  "messages": [
    {"id": "Hello", "message": "Hello", "translation": "Hello"},
    {"id": "Welcome, {Name}", "message": "Welcome, {Name}", "translation": "", "placeholders": [{"id": "Name"}]},
    {"id": "Bye", "message": "Bye", "translation": "Bye"},
    {"id": "{N} items", "message": "{N} items", "translation": {"select": {"feature": "plural"}}}
  ]
}
`)
	writeFile(t, filepath.Join(dir, "es-US", catalog.MessagesFile), `{
  "language": "es-US",
  "messages": [
    {"id": "Hello", "message": "Hello", "translation": "Hola", "fuzzy": true},
    {"id": "Welcome, {Name}", "message": "Welcome, {Name}", "translation": ""}
  ]
}
`)
	return dir
}

func TestPlanFindsMissingAndEmptyMessages(t *testing.T) {
	plans, err := plan(setupLocales(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Fatalf("plan() = %d plans, want en-US and es-US", len(plans))
	}
	base, target := plans[0], plans[1]
	if base.locale != "en-US" || len(base.work) != 1 || base.work[0].text != "Welcome, {Name}" {
		t.Errorf("en-US work = %+v, want the empty Welcome message", base.work)
	}
	var ids []string
	for _, work := range target.work {
		ids = append(ids, string(work.id))
	}
	if strings.Join(ids, ",") != "Welcome, {Name},Bye" {
		t.Errorf("es-US work = %v, want the empty and the absent message", ids)
	}
	if strings.Join(target.skipped, ",") != "{N} items" {
		t.Errorf("es-US skipped = %v, want the plural source", target.skipped)
	}
}

func TestPlanRejectsUnknownLocale(t *testing.T) {
	if _, err := plan(setupLocales(t), []string{"de-DE"}); err == nil || !strings.Contains(err.Error(), `unknown locale "de-DE"`) {
		t.Errorf("plan(de-DE) error = %v, want unknown locale", err)
	}
}

func TestValidateRejectsPlaceholderChanges(t *testing.T) {
	tests := []struct {
		translation string
		returned    bool
		wantReason  string
	}{
		{translation: "Bienvenido, {Name}", returned: true},
		{translation: "{Name}, bienvenido", returned: true},
		{translation: "Bienvenido", returned: true, wantReason: "placeholders differ"},
		{translation: "Bienvenido, {Nombre}", returned: true, wantReason: "placeholders differ"},
		{translation: "Bienvenido, {Name} {Name}", returned: true, wantReason: "placeholders differ"},
		{translation: "", returned: true, wantReason: "empty"},
		{returned: false, wantReason: "no translation"},
	}
	if reason := validate("{Count} files in {Folder}", "{Folder} contiene {Count} archivos", true); reason != "" {
		t.Errorf("validate(reordered placeholders) = %q, want accepted", reason)
	}
	for _, test := range tests {
		reason := validate("Welcome, {Name}", test.translation, test.returned)
		if test.wantReason == "" && reason != "" || !strings.Contains(reason, test.wantReason) {
			t.Errorf("validate(%q) = %q, want %q", test.translation, reason, test.wantReason)
		}
	}
}

// translateRun is one runCommand call in a test.
type translateRun struct {
	cfg     config.LocalizeConfiguration
	locales []string
	dryRun  bool
	format  ui.Format
	getenv  func(string) string
}

func (r translateRun) do(t *testing.T, m model) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &errOut, ui.Options{Format: r.format})
	getenv := r.getenv
	if getenv == nil {
		getenv = withKey
	}
	err := runCommand(context.Background(), u, r.cfg, r.locales, r.dryRun, getenv,
		func(context.Context, string, string) (model, error) { return m, nil })
	return out.String(), errOut.String(), err
}

func runTranslate(t *testing.T, dir string, m model, dryRun bool, getenv func(string) string) (string, string, error) {
	t.Helper()
	return translateRun{cfg: config.LocalizeConfiguration{LocalesDir: dir}, dryRun: dryRun, getenv: getenv}.do(t, m)
}

func withKey(key string) string {
	if key == "GEMINI_API_KEY" {
		return "test-key"
	}
	return ""
}

func TestTranslateWritesBackAndRejectsBadPlaceholders(t *testing.T) {
	dir := setupLocales(t)
	m := &fakeModel{answers: map[string]map[string]string{"es-US": {"Welcome, {Name}": "Bienvenido", "Bye": "Adiós"}}}
	_, stderr, err := runTranslate(t, dir, m, false, withKey)
	if err == nil || !strings.Contains(err.Error(), "rejected 1 translation;") {
		t.Fatalf("runCommand() error = %v, want one rejection", err)
	}
	if !strings.Contains(stderr, `rejected "Welcome, {Name}": placeholders differ`) {
		t.Errorf("stderr = %q, want the rejection reported", stderr)
	}

	es, err := catalog.Load(filepath.Join(dir, "es-US", catalog.MessagesFile))
	if err != nil {
		t.Fatal(err)
	}
	got := map[catalog.ID]any{}
	for _, message := range es.Messages {
		got[message.ID] = message.Translation
	}
	if got["Hello"] != "Hola" || got["Bye"] != "Adiós" || got["Welcome, {Name}"] != "" {
		t.Errorf("es-US translations = %v, want Hola kept, Bye written, Welcome left empty", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "es-US", catalog.MessagesFile))
	if !strings.Contains(string(raw), `"fuzzy": true`) {
		t.Errorf("write-back dropped an existing field:\n%s", raw)
	}

	en, _ := catalog.Load(filepath.Join(dir, "en-US", catalog.MessagesFile))
	if text, _ := en.Messages[1].Text(); text != "Welcome, {Name}" {
		t.Errorf("en-US Welcome translation = %q, want the source text", text)
	}
}

func TestTranslateBatchesRequests(t *testing.T) {
	dir := setupLocales(t)
	m := &fakeModel{answers: map[string]map[string]string{"es-US": {"Welcome, {Name}": "Bienvenido, {Name}", "Bye": "Adiós"}}}
	cfg := config.LocalizeConfiguration{LocalesDir: dir, Translate: config.TranslateConfiguration{BatchSize: 1}}
	if _, _, err := (translateRun{cfg: cfg}).do(t, m); err != nil {
		t.Fatal(err)
	}
	if len(m.batches) != 2 {
		t.Errorf("model saw %d batches, want 2 with batchSize 1", len(m.batches))
	}
}

func TestDryRunLeavesCatalogsUnchanged(t *testing.T) {
	dir := setupLocales(t)
	before, _ := os.ReadFile(filepath.Join(dir, "es-US", catalog.MessagesFile))
	m := &fakeModel{answers: map[string]map[string]string{"es-US": {"Welcome, {Name}": "Bienvenido, {Name}", "Bye": "Adiós"}}}
	stdout, _, err := runTranslate(t, dir, m, true, withKey)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "es-US", catalog.MessagesFile))
	if !bytes.Equal(before, after) {
		t.Error("dry run changed es-US")
	}
	if !strings.Contains(stdout, "Bienvenido, {Name}") || !strings.Contains(stdout, "Adiós") {
		t.Errorf("stdout = %q, want the proposed translations", stdout)
	}
}

func TestTranslateNeedsAPIKeyOnlyForModelWork(t *testing.T) {
	dir := setupLocales(t)
	_, _, err := runTranslate(t, dir, &fakeModel{}, false, func(string) string { return "" })
	if !errors.Is(err, errNoAPIKey) {
		t.Errorf("runCommand() error = %v, want errNoAPIKey", err)
	}
}

func TestTranslateJSONOutput(t *testing.T) {
	dir := setupLocales(t)
	m := &fakeModel{answers: map[string]map[string]string{"es-US": {"Welcome, {Name}": "Bienvenido, {Name}", "Bye": "Adiós"}}}
	out, _, err := translateRun{cfg: config.LocalizeConfiguration{LocalesDir: dir}, locales: []string{"es-US"}, dryRun: true, format: ui.FormatJSON}.do(t, m)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		DryRun  bool     `json:"dryRun"`
		Locales []result `json:"locales"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if !decoded.DryRun || len(decoded.Locales) != 1 || len(decoded.Locales[0].Translated) != 2 || len(decoded.Locales[0].Skipped) != 1 {
		t.Errorf("JSON = %+v, want es-US with 2 translated and 1 skipped", decoded)
	}
}

func TestDecodeNumberedMapsBackToIDs(t *testing.T) {
	messages := []message{{ID: "Hello", Text: "Hello"}, {ID: "Bye", Text: "Bye"}}
	got, err := decodeNumbered(`[{"id":"1","translation":"Adiós"},{"id":"0","translation":"Hola"},{"id":"7","translation":"x"}]`, messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["Hello"] != "Hola" || got["Bye"] != "Adiós" {
		t.Errorf("decodeNumbered() = %v, want Hello and Bye mapped, 7 dropped", got)
	}
	if _, err := decodeNumbered("not json", messages); err == nil {
		t.Error("decodeNumbered(not json) error = nil")
	}
}

// failingModel fails every request for one locale and answers the rest from answers.
type failingModel struct {
	fakeModel
	failLocale string
}

func (f *failingModel) Translate(ctx context.Context, locale, rubric string, messages []message) (map[string]string, error) {
	if locale == f.failLocale {
		return nil, errors.New("gemini: 503 service unavailable")
	}
	return f.fakeModel.Translate(ctx, locale, rubric, messages)
}

func TestFailedBatchRejectsOnlyItsLocale(t *testing.T) {
	dir := setupLocales(t)
	writeFile(t, filepath.Join(dir, "fr-FR", catalog.MessagesFile), `{"language": "fr-FR", "messages": []}`)
	m := &failingModel{failLocale: "fr-FR", fakeModel: fakeModel{answers: map[string]map[string]string{
		"es-US": {"Welcome, {Name}": "Bienvenido, {Name}", "Bye": "Adiós"}}}}
	_, stderr, err := runTranslate(t, dir, m, false, withKey)
	if err == nil || !strings.Contains(err.Error(), "rejected 3 translations") {
		t.Fatalf("runCommand() error = %v, want fr-FR's three messages rejected", err)
	}
	if !strings.Contains(stderr, "503 service unavailable") {
		t.Errorf("stderr = %q, want the request error as the rejection reason", stderr)
	}
	es, _ := catalog.Load(filepath.Join(dir, "es-US", catalog.MessagesFile))
	if len(es.Messages) != 3 {
		t.Errorf("es-US has %d messages, want its translations written despite fr-FR failing", len(es.Messages))
	}
}

func TestJSONModeFailsOnRejections(t *testing.T) {
	dir := setupLocales(t)
	m := &fakeModel{answers: map[string]map[string]string{"es-US": {"Welcome, {Name}": "Bienvenido", "Bye": "Adiós"}}}
	out, _, err := translateRun{cfg: config.LocalizeConfiguration{LocalesDir: dir}, format: ui.FormatJSON}.do(t, m)
	if err == nil || !strings.Contains(err.Error(), "rejected 1 translation") {
		t.Errorf("runCommand(json) error = %v, want the rejection", err)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("stdout = %q, want JSON", out)
	}
}

func TestPlanUsesFirstDuplicateEntry(t *testing.T) {
	dir := setupLocales(t)
	writeFile(t, filepath.Join(dir, "es-US", catalog.MessagesFile), `{"language": "es-US", "messages": [
    {"id": "Bye", "translation": "Adiós"},
    {"id": "Bye", "translation": ""}]}`)
	plans, err := plan(dir, []string{"es-US"})
	if err != nil {
		t.Fatal(err)
	}
	for _, work := range plans[0].work {
		if work.id == "Bye" {
			t.Error("plan scheduled Bye although its first entry is translated")
		}
	}
}

func TestDecodeNumberedDropsDuplicateAnswers(t *testing.T) {
	messages := []message{{ID: "Hello", Text: "Hello"}, {ID: "Bye", Text: "Bye"}}
	got, err := decodeNumbered(`[{"id":"0","translation":"Hola"},{"id":"0","translation":"Adiós"},{"id":"1","translation":"Adiós"}]`, messages)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Hello"]; ok || got["Bye"] != "Adiós" {
		t.Errorf("decodeNumbered() = %v, want Hello dropped for its two answers", got)
	}
}

func TestPlanSkipsHelperDirectories(t *testing.T) {
	dir := setupLocales(t)
	if err := os.MkdirAll(filepath.Join(dir, "registry"), 0o755); err != nil {
		t.Fatal(err)
	}
	plans, err := plan(dir, nil)
	if err != nil || len(plans) != 2 {
		t.Errorf("plan() = %d plans, %v; want the helper directory skipped", len(plans), err)
	}
}

func TestJSONListsEmptyArrays(t *testing.T) {
	dir := setupLocales(t)
	writeFile(t, filepath.Join(dir, "es-US", catalog.MessagesFile), `{"language": "es-US", "messages": [
    {"id": "Hello", "translation": "Hola"}, {"id": "Welcome, {Name}", "translation": "Hola, {Name}"}, {"id": "Bye", "translation": "Adiós"}]}`)
	out, _, err := translateRun{cfg: config.LocalizeConfiguration{LocalesDir: dir}, locales: []string{"es-US"}, format: ui.FormatJSON}.do(t, &fakeModel{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "null") {
		t.Errorf("stdout = %s, want [] instead of null", out)
	}
}
