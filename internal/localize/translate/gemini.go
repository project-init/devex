package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/genai"
)

// defaultModel is the Gemini model used when the config names none.
const defaultModel = "gemini-2.5-flash"

// gemini translates through Google's official Gemini SDK.
type gemini struct {
	client *genai.Client
	model  string
}

func newGemini(ctx context.Context, apiKey, model string) (*gemini, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
		// Retry rate limits and server errors with the SDK's backoff before a batch fails.
		HTTPOptions: genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr[int32](5)}},
	})
	if err != nil {
		return nil, fmt.Errorf("create Gemini client: %w", err)
	}
	return &gemini{client: client, model: model}, nil
}

// translationSchema asks Gemini for [{"id": ..., "translation": ...}].
var translationSchema = &genai.Schema{
	Type: genai.TypeArray,
	Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"id":          {Type: genai.TypeString},
			"translation": {Type: genai.TypeString},
		},
		Required: []string{"id", "translation"},
	},
}

func (g *gemini) Translate(ctx context.Context, locale, rubric string, messages []message) (map[string]string, error) {
	// Number the messages instead of sending each id, which usually repeats the text and would
	// double the tokens Gemini reads and writes.
	numbered := make([]message, len(messages))
	for index, msg := range messages {
		numbered[index] = message{ID: strconv.Itoa(index), Text: msg.Text}
	}
	request, err := json.Marshal(numbered)
	if err != nil {
		return nil, err
	}
	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(instructions(locale, rubric), genai.RoleUser),
		ResponseMIMEType:  "application/json",
		ResponseSchema:    translationSchema,
		Temperature:       genai.Ptr[float32](0.2),
	}
	response, err := g.client.Models.GenerateContent(ctx, g.model, genai.Text(string(request)), config)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	return decodeNumbered(response.Text(), messages)
}

// decodeNumbered maps Gemini's numbered answers back to message ids. It drops numbers outside
// the batch and numbers answered twice, which then count as missing translations.
func decodeNumbered(text string, messages []message) (map[string]string, error) {
	var translations []struct {
		ID          string `json:"id"`
		Translation string `json:"translation"`
	}
	if err := json.Unmarshal([]byte(text), &translations); err != nil {
		return nil, fmt.Errorf("gemini returned malformed JSON: %w", err)
	}
	result := make(map[string]string, len(translations))
	answered := make(map[int]int, len(translations))
	for _, translation := range translations {
		index, err := strconv.Atoi(translation.ID)
		if err != nil || index < 0 || index >= len(messages) {
			continue
		}
		answered[index]++
		result[messages[index].ID] = translation.Translation
	}
	for index, count := range answered {
		if count > 1 {
			delete(result, messages[index].ID)
		}
	}
	return result, nil
}

// instructions tells the model how to translate into locale, with the project's rubric last.
func instructions(locale, rubric string) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Translate user interface strings from English (en-US) into %s.\n", locale)
	prompt.WriteString("The input is a JSON array of {\"id\", \"text\"} objects. Return one {\"id\", \"translation\"} object per input, with the id unchanged.\n")
	prompt.WriteString("Keep every placeholder in braces, such as {Name}, exactly as written: same spelling, same count. Translate the words around them.\n")
	prompt.WriteString("Keep HTML tags, punctuation that carries meaning, and leading or trailing whitespace.\n")
	if strings.TrimSpace(rubric) != "" {
		prompt.WriteString("\nFollow this project rubric for terminology and legal wording:\n\n")
		prompt.WriteString(rubric)
	}
	return prompt.String()
}
