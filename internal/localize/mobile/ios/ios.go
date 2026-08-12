package ios

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/localize/config"
)

const (
	defaultSourceLanguage = "en-US"
	outCatalogName        = "out.gotext.json"
)

var (
	registryCallPattern = regexp.MustCompile(`^\s*p\.Sprintf\("([^"]+)"\)$`)
	registryIDPattern   = regexp.MustCompile(`// id: (.+)$`)
	placeholderPattern  = regexp.MustCompile(`\{[A-Za-z0-9_]+\}`)
	translationPattern  = regexp.MustCompile(`translate\("([^"\r\n]+)"`)
	stringPattern       = regexp.MustCompile(`"([^"\r\n]*)"`)
)

type catalog struct {
	Language string    `json:"language"`
	Messages []message `json:"messages"`
}

// gotextID handles the gotext format where "id" may be a plain string or a
// [key, fallbackText] array; we always use the first element as the ID.
type gotextID string

func (g *gotextID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*g = gotextID(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) == 0 {
		return fmt.Errorf("gotext id array is empty")
	}
	*g = gotextID(arr[0])
	return nil
}

type message struct {
	ID          gotextID `json:"id"`
	Translation any      `json:"translation"`
}

type bundle struct {
	Language string          `json:"language"`
	Messages orderedMessages `json:"messages"`
}

type localizedMessage struct {
	id          string
	translation string
}

type orderedMessages []localizedMessage

type generatedBundle struct {
	language string
	data     []byte
	count    int
}

func Generate(
	localesDir string,
	sourceLanguage string,
	registryPath string,
	cfg config.IOSConfiguration,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if err := validateConfig(localesDir, registryPath, cfg); err != nil {
		return err
	}

	if sourceLanguage == "" {
		sourceLanguage = defaultSourceLanguage
	}

	ids, err := loadRegistryIDs(registryPath)
	if err != nil {
		return err
	}

	directories, err := filepath.Glob(filepath.Join(localesDir, "*/"))
	if err != nil {
		return err
	}
	if len(directories) == 0 {
		return fmt.Errorf("no locale directories found in %s", localesDir)
	}

	bundles := make([]generatedBundle, 0, len(directories))
	for _, directory := range directories {
		language := filepath.Base(filepath.Clean(directory))
		catalogPath := filepath.Join(directory, outCatalogName)
		cat, err := loadCatalog(catalogPath)
		if err != nil {
			return err
		}

		if err := validateCatalog(catalogPath, cat, ids, language == sourceLanguage); err != nil {
			return err
		}

		messages := make(orderedMessages, 0)
		for _, msg := range cat.Messages {
			translation, ok := msg.Translation.(string)
			if !ok || translation == "" {
				continue
			}
			if _, registered := ids[string(msg.ID)]; registered {
				messages = append(messages, localizedMessage{id: string(msg.ID), translation: translation})
			}
		}

		data, err := json.MarshalIndent(bundle{Language: cat.Language, Messages: messages}, "", "  ")
		if err != nil {
			return err
		}
		bundles = append(bundles, generatedBundle{
			language: language,
			data:     append(data, '\n'),
			count:    len(messages),
		})
	}

	if err := validateSourceUsage(cfg.SourceDir, registryPath, ids, stderr); err != nil {
		return err
	}

	if err := writeBundles(cfg.OutputDir, bundles, stdout); err != nil {
		return err
	}
	return nil
}

func (messages orderedMessages) MarshalJSON() ([]byte, error) {
	var output bytes.Buffer
	output.WriteByte('{')
	for index, msg := range messages {
		if index > 0 {
			output.WriteByte(',')
		}

		id, err := marshalJSONString(msg.id)
		if err != nil {
			return nil, err
		}
		translation, err := marshalJSONString(msg.translation)
		if err != nil {
			return nil, err
		}
		output.Write(id)
		output.WriteByte(':')
		output.Write(translation)
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func marshalJSONString(value string) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

func validateConfig(localesDir string, registryPath string, cfg config.IOSConfiguration) error {
	missing := make([]string, 0, 4)
	if localesDir == "" {
		missing = append(missing, "localize.localesDir")
	}
	if registryPath == "" {
		missing = append(missing, "localize.mobile.registryPath")
	}
	if cfg.SourceDir == "" {
		missing = append(missing, "localize.mobile.ios.sourceDir")
	}
	if cfg.OutputDir == "" {
		missing = append(missing, "localize.mobile.ios.outputDir")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func loadRegistryIDs(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	ids := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if matches := registryCallPattern.FindStringSubmatch(line); matches != nil {
			ids[matches[1]] = struct{}{}
		}
		if matches := registryIDPattern.FindStringSubmatch(line); matches != nil {
			ids[matches[1]] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func loadCatalog(path string) (*catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("missing %s", path)
		}
		return nil, err
	}

	var cat catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cat, nil
}

func validateCatalog(path string, cat *catalog, ids map[string]struct{}, source bool) error {
	for _, msg := range cat.Messages {
		if _, object := msg.Translation.(map[string]any); object {
			return fmt.Errorf("%s contains plural/select translations; the iOS converter does not support them", path)
		}
	}

	if source {
		have := make(map[string]struct{}, len(cat.Messages))
		for _, msg := range cat.Messages {
			have[string(msg.ID)] = struct{}{}
		}

		missing := difference(ids, have)
		if len(missing) > 0 {
			return fmt.Errorf(
				"registry ids missing from %s (stale annotation or catalog?):\n  %s",
				path,
				strings.Join(missing, "\n  "),
			)
		}
	}

	badTokens := make([]string, 0)
	for _, msg := range cat.Messages {
		translation, ok := msg.Translation.(string)
		if !ok || translation == "" {
			continue
		}

		allowed := toSet(placeholderPattern.FindAllString(string(msg.ID), -1))
		unexpected := difference(toSet(placeholderPattern.FindAllString(translation, -1)), allowed)
		if len(unexpected) > 0 {
			badTokens = append(badTokens, fmt.Sprintf("%s -> %v", string(msg.ID), unexpected))
		}
	}
	if len(badTokens) > 0 {
		return fmt.Errorf(
			"%s has translations using placeholders their id lacks:\n  %s",
			path,
			strings.Join(badTokens, "\n  "),
		)
	}
	return nil
}

func validateSourceUsage(sourceDir string, registryPath string, ids map[string]struct{}, stderr io.Writer) error {
	usedKeys := make(map[string]struct{})
	sourceLiterals := make(map[string]struct{})

	err := filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".swift" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, matches := range translationPattern.FindAllStringSubmatch(text, -1) {
			usedKeys[matches[1]] = struct{}{}
		}
		for _, matches := range stringPattern.FindAllStringSubmatch(text, -1) {
			sourceLiterals[matches[1]] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}

	undeclared := difference(usedKeys, ids)
	if len(undeclared) > 0 {
		return fmt.Errorf(
			"swift translate(...) keys missing from %s:\n  %s",
			registryPath,
			strings.Join(undeclared, "\n  "),
		)
	}

	unused := difference(ids, sourceLiterals)
	for _, id := range unused {
		_, _ = fmt.Fprintf(stderr, "warning: registry string not referenced by Swift code: %s\n", id)
	}
	if len(unused) > 0 {
		_, _ = fmt.Fprintf(stderr, "warning: %d unreferenced registry string(s) — dead copy candidates\n", len(unused))
	}
	return nil
}

func writeBundles(outputDir string, bundles []generatedBundle, stdout io.Writer) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}

	existing, err := filepath.Glob(filepath.Join(outputDir, "l10n-*.json"))
	if err != nil {
		return err
	}
	for _, path := range existing {
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	for _, generated := range bundles {
		path := filepath.Join(outputDir, "l10n-"+generated.language+".json")
		if err := os.WriteFile(path, generated.data, 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "wrote %s (%d messages)\n", path, generated.count)
	}
	return nil
}

func toSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func difference(left map[string]struct{}, right map[string]struct{}) []string {
	values := make([]string, 0)
	for value := range left {
		if _, ok := right[value]; !ok {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}
