package mcpreg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// object is a JSON object that keeps its keys in file order and its values
// as the bytes it read, so a rewrite of one key leaves every other value
// exactly as the agent wrote it (numbers included).
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object { return &object{vals: map[string]json.RawMessage{}} }

// parseObject reads a JSON object. Empty input is an empty object.
func parseObject(b []byte) (*object, error) {
	o := newObject()
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := tok.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("trailing data after the JSON object")
	}
	return o, nil
}

func (o *object) get(k string) (json.RawMessage, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// child reads key k as an object; absent or null is an empty object, any
// other type is an error.
func (o *object) child(k string) (*object, error) {
	v, ok := o.get(k)
	if !ok || string(bytes.TrimSpace(v)) == "null" {
		return newObject(), nil
	}
	c, err := parseObject(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k, err)
	}
	return c, nil
}

func (o *object) setChild(k string, c *object) { o.set(k, c.compact()) }

// compact is the object as compact JSON, keys in order.
func (o *object) compact() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		var c bytes.Buffer
		if err := json.Compact(&c, o.vals[k]); err != nil {
			b.Write(o.vals[k])
		} else {
			b.Write(c.Bytes())
		}
	}
	b.WriteByte('}')
	return b.Bytes()
}

// indented is the object as JSON indented by two spaces, as Claude Code
// writes its files, with a final newline.
func (o *object) indented() []byte {
	var b bytes.Buffer
	if err := json.Indent(&b, o.compact(), "", "  "); err != nil {
		return o.compact()
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// marshal is json.Marshal without HTML escaping, so a URL keeps its & and
// a value reads in the file as the user wrote it.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func mustRaw(v any) json.RawMessage {
	b, err := marshal(v)
	if err != nil {
		// Every value here came from encoding/json or BurntSushi/toml, which
		// only produce encodable values.
		return json.RawMessage("null")
	}
	return b
}

// canonical is v as JSON with object keys sorted, numbers as written, for
// comparing values by content. Raw JSON is decoded first.
func canonical(v any) string {
	switch x := v.(type) {
	case json.RawMessage:
		var d any
		dec := json.NewDecoder(bytes.NewReader(x))
		dec.UseNumber()
		if err := dec.Decode(&d); err != nil {
			return "\x00invalid:" + string(x)
		}
		v = d
	case []byte:
		return canonical(json.RawMessage(x))
	}
	b, err := marshal(normalize(v))
	if err != nil {
		return "\x00unencodable"
	}
	return string(b)
}

// normalize turns the shapes BurntSushi/toml decodes (int64, []any of
// map[string]any, []map[string]any) into ones that encode the same way as
// their JSON twins; json.Marshal already sorts map keys.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalize(vv)
		}
		return out
	case []map[string]any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalize(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalize(vv)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = vv
		}
		return out
	case int:
		return json.Number(fmt.Sprint(x))
	case int64:
		return json.Number(fmt.Sprint(x))
	}
	return v
}

func equal(a, b any) bool { return canonical(a) == canonical(b) }

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
