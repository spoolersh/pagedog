package jsonschema

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		exp  Schema
	}{
		{
			name: "struct",
			in:   point{},
			exp: Schema{
				Type: "object",
				Properties: map[string]*Schema{
					"x": {
						Type:        "integer",
						Description: "the x",
					},
					"y": {
						Type: "integer",
					},
				},
				Required: []string{"x", "y"},
			},
		},
		{
			name: "every kind",
			in:   kinds{},
			exp: Schema{
				Type: "object",
				Properties: map[string]*Schema{
					"B": {
						Type: "boolean",
					},
					"I": {
						Type: "integer",
					},
					"U": {
						Type: "integer",
					},
					"F": {
						Type: "number",
					},
					"S": {
						Type: "string",
					},
					"T": {
						Type: "string",
					},
					"R": {
						Type: "string",
					},
					"L": {
						Type: "array",
						Items: &Schema{
							Type: "string",
						},
					},
					"P": {
						Type: "object",
						Properties: map[string]*Schema{
							"x": {
								Type:        "integer",
								Description: "the x",
							},
							"y": {
								Type: "integer",
							},
						},
						Required: []string{"x", "y"},
					},
					"M": {
						Type: "object",
					},
					"A": {},
					"o": {
						Type: "integer",
					},
					"z": {
						Type: "integer",
					},
				},
				Required: []string{"B", "I", "U", "F", "S", "T", "R", "L", "P", "M", "A"},
			},
		},
		{
			name: "embedded struct",
			in:   embeds{},
			exp: Schema{
				Type: "object",
				Properties: map[string]*Schema{
					"x": {
						Type:        "integer",
						Description: "the x",
					},
					"y": {
						Type: "integer",
					},
					"name": {
						Type: "string",
					},
				},
				Required: []string{"x", "y", "name"},
			},
		},
		{
			name: "scalar",
			in:   "",
			exp: Schema{
				Type: "string",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act := *of(reflect.TypeOf(tc.in), nil)
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"schema: %s",
					cmp.Diff(tc.exp, act),
				)
			}
		})
	}
}

func TestOfPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
	}{
		{
			name: "cycle",
			in:   loop{},
		},
		{
			name: "chan",
			in:   make(chan int),
		},
		{
			name: "func",
			in:   func() {},
		},
		{
			name: "map keyed by int",
			in:   map[int]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic; want one")
				}
			}()
			of(reflect.TypeOf(tc.in), nil)
		})
	}
}

func TestFrom(t *testing.T) {
	exp := Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"x": {
				Type:        "integer",
				Description: "the x",
			},
			"y": {
				Type: "integer",
			},
		},
		Required: []string{"x", "y"},
	}
	act := From[point]()
	if !cmp.Equal(exp, act) {
		t.Errorf(
			"schema: %s",
			cmp.Diff(exp, act),
		)
	}
}

type point struct {
	X int `json:"x" jsonschema:"the x"`
	Y int `json:"y"`
}

type kinds struct {
	B bool
	I int
	U uint8
	F float64
	S string
	T time.Time
	R []byte
	L []string
	P *point
	M map[string]int
	A any

	O int `json:"o,omitempty"`
	Z int `json:"z,omitzero"`

	Skip int `json:"-"`
	skip int
}

type embeds struct {
	point
	Name string `json:"name"`
}

type loop struct {
	Next *loop `json:"next"`
}
