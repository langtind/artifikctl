package artifik

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// The web client gets its data as a React Router turbo-stream: the first
// line is a flat JSON array where element 0 is the root, and objects and
// arrays point at other elements by index. reference/README.md has the
// format as it was observed.

// errRedirect is a loader that answered with a redirect, which for a page
// behind the login means the login was refused.
type errRedirect struct{ To string }

func (e errRedirect) Error() string { return "redirected to " + e.To }

// decodeTurbo turns the first line of a turbo-stream into plain JSON.
func decodeTurbo(body []byte) (json.RawMessage, error) {
	line, _, _ := bytes.Cut(body, []byte("\n"))
	var flat []json.RawMessage
	if err := json.Unmarshal(line, &flat); err != nil {
		return nil, fmt.Errorf("not a turbo-stream: %w", err)
	}
	if len(flat) == 0 {
		return nil, errors.New("an empty turbo-stream")
	}
	d := turbo{flat: flat, done: map[int]any{}}

	var head []json.RawMessage
	if json.Unmarshal(flat[0], &head) == nil && len(head) > 0 {
		var tag string
		if json.Unmarshal(head[0], &tag) == nil && tag == "SingleFetchRedirect" {
			to := ""
			if len(flat) > 1 {
				if v, err := d.value(1, 0); err == nil {
					if m, ok := v.(map[string]any); ok {
						to, _ = m["redirect"].(string)
					}
				}
			}
			return nil, errRedirect{To: to}
		}
	}

	root, err := d.value(0, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

type turbo struct {
	flat []json.RawMessage
	done map[int]any
}

// The highest nesting a decode follows, so that a cycle cannot hang it.
const maxDepth = 200

func (d *turbo) ref(raw json.RawMessage, depth int) (any, error) {
	var i int
	if err := json.Unmarshal(raw, &i); err != nil {
		return nil, fmt.Errorf("a reference that is not a number: %s", raw)
	}
	if i < 0 {
		// -1 hole, -5 null, -7 undefined. The numbers that cannot be JSON are
		// written as null too.
		return nil, nil
	}
	return d.value(i, depth+1)
}

func (d *turbo) value(i, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("a turbo-stream nested too deep")
	}
	if i >= len(d.flat) {
		return nil, fmt.Errorf("a reference past the end: %d", i)
	}
	if v, ok := d.done[i]; ok {
		return v, nil
	}
	raw := bytes.TrimSpace(d.flat[i])
	var out any
	switch {
	case len(raw) > 0 && raw[0] == '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(obj))
		for k, v := range obj {
			key, err := d.key(k)
			if err != nil {
				return nil, err
			}
			if m[key], err = d.ref(v, depth); err != nil {
				return nil, err
			}
		}
		out = m
	case len(raw) > 0 && raw[0] == '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		if typed, ok, err := d.typed(items); ok || err != nil {
			return typed, err
		}
		list := make([]any, len(items))
		for n, item := range items {
			v, err := d.ref(item, depth)
			if err != nil {
				return nil, err
			}
			list[n] = v
		}
		out = list
	default:
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
	}
	d.done[i] = out
	return out, nil
}

// key reads "_7" as the string at index 7.
func (d *turbo) key(k string) (string, error) {
	if len(k) < 2 || k[0] != '_' {
		return "", fmt.Errorf("an object key that is not a reference: %q", k)
	}
	i, err := strconv.Atoi(k[1:])
	if err != nil || i < 0 || i >= len(d.flat) {
		return "", fmt.Errorf("an object key that is not a reference: %q", k)
	}
	var s string
	if err := json.Unmarshal(d.flat[i], &s); err != nil {
		return "", fmt.Errorf("an object key that is not a string: %q", k)
	}
	return s, nil
}

// typed reads an array that starts with a one letter type, such as
// ["D", ...] for a date. Only dates have been seen in Artifik's answers.
func (d *turbo) typed(items []json.RawMessage) (any, bool, error) {
	if len(items) < 2 {
		return nil, false, nil
	}
	var tag string
	if json.Unmarshal(items[0], &tag) != nil || len(tag) != 1 {
		return nil, false, nil
	}
	if tag == "D" {
		var v any
		if err := json.Unmarshal(items[1], &v); err != nil {
			return nil, true, err
		}
		if ms, ok := v.(float64); ok {
			return time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339), true, nil
		}
		return v, true, nil
	}
	// Anything else is not used by the client, and is left out.
	return nil, true, nil
}
