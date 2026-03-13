package main

import (
	"log/slog"
	"os"
	"strings"
)

func initLogger() {
	level := &slog.LevelVar{}
	level.Set(parseLogLevel(os.Getenv("LOG_LEVEL")))
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
}

func parseLogLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
