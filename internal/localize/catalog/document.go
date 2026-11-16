package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/project-init/devex/internal/atomicfile"
)

// Document edits a catalog file without disturbing what it doesn't change: every entry keeps its
// fields, field order, and id form, and unknown fields survive. Save reindents the file with
// the indent it was read with.
type Document struct {
	top      object
	messages []object
	index    map[ID]int // message id -> position of its first entry in messages
	indent   string
	newline  bool // the file ended with a newline
}

// LoadDocument reads the catalog at path for editing.
func LoadDocument(path string) (*Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	doc := &Document{top: top, indent: detectIndent(raw), index: map[ID]int{}, newline: bytes.HasSuffix(raw, []byte("\n"))}
	if messages, ok := top.get("messages"); ok && string(bytes.TrimSpace(messages)) != "null" {
		if doc.messages, err = parseObjects(messages); err != nil {
			return nil, fmt.Errorf("parse %s messages: %w", path, err)
		}
	}
	for position, message := range doc.messages {
		var id ID
		if rawID, ok := message.get("id"); ok && json.Unmarshal(rawID, &id) == nil {
			if _, seen := doc.index[id]; !seen {
				doc.index[id] = position
			}
		}
	}
	return doc, nil
}

// SetTranslation sets the translation of the entry with id and reports whether one exists.
func (d *Document) SetTranslation(id ID, text string) (bool, error) {
	index, ok := d.index[id]
	if !ok {
		return false, nil
	}
	value, err := marshalString(text)
	if err != nil {
		return false, err
	}
	d.messages[index] = d.messages[index].set("translation", value)
	return true, nil
}

// AppendFrom adds a copy of source's entry for id, translated as text. source is usually the
// base locale, so the new entry carries its message, placeholders, and comments.
func (d *Document) AppendFrom(source *Document, id ID, text string) error {
	index, ok := source.index[id]
	if !ok {
		return fmt.Errorf("message %q not found in the source catalog", id)
	}
	value, err := marshalString(text)
	if err != nil {
		return err
	}
	d.index[id] = len(d.messages)
	d.messages = append(d.messages, source.messages[index].set("translation", value))
	return nil
}

// Save writes the document to path atomically, keeping the file's permissions.
func (d *Document) Save(path string) error {
	var messages bytes.Buffer
	messages.WriteByte('[')
	for index, message := range d.messages {
		if index > 0 {
			messages.WriteByte(',')
		}
		messages.Write(message.marshal())
	}
	messages.WriteByte(']')
	top := d.top.set("messages", messages.Bytes())

	var out bytes.Buffer
	if err := json.Indent(&out, top.marshal(), "", d.indent); err != nil {
		return err
	}
	if d.newline {
		out.WriteByte('\n')
	}

	return atomicfile.Write(path, out.Bytes())
}

// field is one key and its raw JSON value, kept in file order.
type field struct {
	key   string
	value json.RawMessage
}

type object []field

func (o object) get(key string) (json.RawMessage, bool) {
	for _, f := range o {
		if f.key == key {
			return f.value, true
		}
	}
	return nil, false
}

// set returns o with key's value replaced, or appended when o lacks key.
func (o object) set(key string, value json.RawMessage) object {
	for index, f := range o {
		if f.key == key {
			updated := append(object(nil), o...)
			updated[index].value = value
			return updated
		}
	}
	return append(append(object(nil), o...), field{key: key, value: value})
}

func (o object) marshal() []byte {
	var out bytes.Buffer
	out.WriteByte('{')
	for index, f := range o {
		if index > 0 {
			out.WriteByte(',')
		}
		key, _ := marshalString(f.key)
		out.Write(key)
		out.WriteByte(':')
		out.Write(f.value)
	}
	out.WriteByte('}')
	return out.Bytes()
}

func parseObject(raw []byte) (object, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := expectDelim(decoder, '{'); err != nil {
		return nil, err
	}
	var result object
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected an object key, got %v", token)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		result = append(result, field{key: key, value: value})
	}
	return result, expectDelim(decoder, '}')
}

func parseObjects(raw []byte) ([]object, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := expectDelim(decoder, '['); err != nil {
		return nil, err
	}
	var result []object
	for decoder.More() {
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		entry, err := parseObject(value)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, expectDelim(decoder, ']')
}

func expectDelim(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("expected %q, got %v", want, token)
	}
	return nil
}

// marshalString encodes text as a JSON string the way gotext does, through encoding/json's
// default HTML escaping.
func marshalString(text string) (json.RawMessage, error) {
	return json.Marshal(text)
}

// detectIndent returns the whitespace that starts the file's first indented line, or two
// spaces.
func detectIndent(raw []byte) string {
	for _, line := range bytes.Split(raw, []byte("\n"))[1:] {
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) < len(line) && len(trimmed) > 0 {
			return string(line[:len(line)-len(trimmed)])
		}
	}
	return "  "
}
