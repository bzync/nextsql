package json

import (
	"bytes"
	stdjson "encoding/json"
	"strconv"
)

func decodeGeneric(doc []byte) (any, error) {
	text, err := ToText(doc)
	if err != nil {
		return nil, err
	}
	dec := stdjson.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, formatErr("json.decodeGeneric", "invalid canonical JSON")
	}
	return value, nil
}

func encodeGeneric(value any) ([]byte, error) {
	text, err := stdjson.Marshal(value)
	if err != nil {
		return nil, argErr("json.encodeGeneric", "value cannot be encoded")
	}
	return FromText(text)
}

// Set installs replacement at path. Missing object keys are created; array
// indexes must already exist. The root is replaced when path is empty.
func Set(doc []byte, path []string, replacement []byte) ([]byte, error) {
	root, err := decodeGeneric(doc)
	if err != nil {
		return nil, err
	}
	value, err := decodeGeneric(replacement)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return encodeGeneric(value)
	}
	cur := root
	for _, part := range path[:len(path)-1] {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				next = map[string]any{}
				node[part] = next
			}
			cur = next
		case []any:
			i, err := pathIndex(part, len(node))
			if err != nil {
				return nil, err
			}
			cur = node[i]
		default:
			return nil, argErr("json.Set", "path traverses a scalar")
		}
	}
	last := path[len(path)-1]
	switch node := cur.(type) {
	case map[string]any:
		node[last] = value
	case []any:
		i, err := pathIndex(last, len(node))
		if err != nil {
			return nil, err
		}
		node[i] = value
	default:
		return nil, argErr("json.Set", "path parent is a scalar")
	}
	return encodeGeneric(root)
}

// Remove deletes an object key or array element. A missing path is a no-op.
func Remove(doc []byte, path []string) ([]byte, error) {
	root, err := decodeGeneric(doc)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return FromText([]byte("null"))
	}
	cur := root
	for _, part := range path[:len(path)-1] {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return append([]byte(nil), doc...), nil
			}
			cur = next
		case []any:
			i, err := pathIndex(part, len(node))
			if err != nil {
				return append([]byte(nil), doc...), nil
			}
			cur = node[i]
		default:
			return append([]byte(nil), doc...), nil
		}
	}
	last := path[len(path)-1]
	switch node := cur.(type) {
	case map[string]any:
		delete(node, last)
	case []any:
		i, err := pathIndex(last, len(node))
		if err != nil {
			return append([]byte(nil), doc...), nil
		}
		node = append(node[:i], node[i+1:]...)
		if len(path) == 1 {
			root = node
		} else {
			if err := replaceContainer(root, path[:len(path)-1], node); err != nil {
				return nil, err
			}
		}
	}
	return encodeGeneric(root)
}

func replaceContainer(root any, path []string, value any) error {
	cur := root
	for _, part := range path[:len(path)-1] {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[part]
		case []any:
			i, err := pathIndex(part, len(node))
			if err != nil {
				return err
			}
			cur = node[i]
		}
	}
	last := path[len(path)-1]
	switch node := cur.(type) {
	case map[string]any:
		node[last] = value
	case []any:
		i, err := pathIndex(last, len(node))
		if err != nil {
			return err
		}
		node[i] = value
	}
	return nil
}

func pathIndex(part string, n int) (int, error) {
	i, err := strconv.Atoi(part)
	if err != nil || i < 0 || i >= n {
		return 0, argErr("json.path", "array index out of range")
	}
	return i, nil
}

// Contains reports recursive JSON containment. Objects use subset semantics;
// every target array element must be contained by some source element.
func Contains(doc, target []byte) (bool, error) {
	a, err := decodeGeneric(doc)
	if err != nil {
		return false, err
	}
	b, err := decodeGeneric(target)
	if err != nil {
		return false, err
	}
	return containsValue(a, b), nil
}

func containsValue(a, b any) bool {
	switch want := b.(type) {
	case map[string]any:
		got, ok := a.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range want {
			candidate, ok := got[key]
			if !ok || !containsValue(candidate, value) {
				return false
			}
		}
		return true
	case []any:
		got, ok := a.([]any)
		if !ok {
			return false
		}
		for _, value := range want {
			found := false
			for _, candidate := range got {
				if containsValue(candidate, value) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case stdjson.Number:
		got, ok := a.(stdjson.Number)
		return ok && got.String() == want.String()
	default:
		return a == b
	}
}
