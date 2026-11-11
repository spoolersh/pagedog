// Package jsonschema holds a subset of JSON Schema and describes Go types in
// it.
package jsonschema

import (
	"encoding"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Schema is a JSON Schema document, in the subset of keywords this package
// handles.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Description string             `json:"description,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Minimum     *int               `json:"minimum,omitempty"`
	Maximum     *int               `json:"maximum,omitempty"`
}

// From describes T the way encoding/json marshals it.
//
// A struct is an object whose properties are its exported fields under their
// JSON names, each required unless tagged omitempty or omitzero, with a
// `jsonschema` tag as its description. A slice or an array is an array, a map
// keyed by strings is an object, a pointer is what it points to, and a type
// that marshals itself as text, such as [time.Time], is a string. Enum,
// Minimum and Maximum are never set: set them on the result.
//
// It panics on a type JSON cannot carry: a cycle, a map keyed by anything but
// a string, a func, a chan or a complex number.
func From[T any]() Schema {
	return *of(reflect.TypeFor[T](), nil)
}

var textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()

// of describes t; seen are the types being described above it, to catch a
// cycle.
func of(t reflect.Type, seen []reflect.Type) *Schema {
	if slices.Contains(seen, t) {
		panic(fmt.Sprintf("jsonschema: %s contains itself", t))
	}
	seen = append(seen, t)

	if t.Implements(textMarshaler) || reflect.PointerTo(t).Implements(textMarshaler) {
		return &Schema{
			Type: "string",
		}
	}
	switch t.Kind() {
	case reflect.Bool:
		return &Schema{
			Type: "boolean",
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return &Schema{
			Type: "integer",
		}
	case reflect.Float32, reflect.Float64:
		return &Schema{
			Type: "number",
		}
	case reflect.String:
		return &Schema{
			Type: "string",
		}
	case reflect.Pointer:
		return of(t.Elem(), seen)
	case reflect.Interface:
		// Anything.
		return &Schema{}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			// Bytes travel base64 encoded.
			return &Schema{
				Type: "string",
			}
		}
		fallthrough
	case reflect.Array:
		return &Schema{
			Type:  "array",
			Items: of(t.Elem(), seen),
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			break
		}
		return &Schema{
			Type: "object",
		}
	case reflect.Struct:
		return object(t, seen)
	}
	panic(fmt.Sprintf("jsonschema: %s cannot be described", t))
}

func object(t reflect.Type, seen []reflect.Type) *Schema {
	s := &Schema{
		Type: "object",
	}
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" && embedded(f) {
			// Its fields are visible on their own.
			continue
		}
		if name == "" {
			name = f.Name
		}
		p := of(f.Type, seen)
		p.Description = f.Tag.Get("jsonschema")
		if s.Properties == nil {
			s.Properties = make(map[string]*Schema)
		}
		s.Properties[name] = p

		optional := slices.ContainsFunc(strings.Split(opts, ","), func(opt string) bool {
			return opt == "omitempty" || opt == "omitzero"
		})
		if !optional {
			s.Required = append(s.Required, name)
		}
	}
	return s
}

// embedded tells an embedded struct, or pointer to one, from a field.
func embedded(f reflect.StructField) bool {
	if !f.Anonymous {
		return false
	}
	t := f.Type
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}
