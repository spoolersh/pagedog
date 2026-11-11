package main

import (
	"flag"
	"fmt"
	"log/slog"
	"strings"
)

// LogLevelVar returns a [flag.Value] that sets level from its name: debug,
// info, warn, error or off.
func LogLevelVar(level *slog.Level) flag.Value {
	return (*levelFlag)(level)
}

type levelFlag slog.Level

const levelOff = levelFlag(slog.LevelError + 8)

func (l *levelFlag) String() string {
	if *l >= levelOff {
		return "off"
	}
	return strings.ToLower(slog.Level(*l).String())
}

func (l *levelFlag) Set(s string) error {
	switch strings.ToLower(s) {
	case "debug":
		*l = levelFlag(slog.LevelDebug)
	case "info":
		*l = levelFlag(slog.LevelInfo)
	case "warn":
		*l = levelFlag(slog.LevelWarn)
	case "error":
		*l = levelFlag(slog.LevelError)
	case "off":
		*l = levelOff
	default:
		return fmt.Errorf(
			"unexpected log level: %q",
			s,
		)
	}
	return nil
}
