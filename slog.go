package main

import (
	"log/slog"
	"os"
	"sync"
)

var initDebugLog sync.Once

func initDebugLogOnce() {
	initDebugLog.Do(func() {
		if os.Getenv("CCMD_DEBUG") != "1" {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
			return
		}
		f, err := os.OpenFile("/tmp/ccmd-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})))
	})
}
