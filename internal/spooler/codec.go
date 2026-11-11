package spooler

import (
	"encoding/json"
)

func decode[T any](fn func([]byte) (T, error), p []byte) (T, error) {
	if fn != nil {
		return fn(p)
	}
	var v T
	err := json.Unmarshal(p, &v)
	return v, err
}

func encode[T any](fn func(T) ([]byte, error), v T) ([]byte, error) {
	if fn != nil {
		return fn(v)
	}
	return json.Marshal(v)
}
