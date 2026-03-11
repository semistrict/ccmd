package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	slogFile *os.File
	slogOnce sync.Once
)

func slogEnabled() bool {
	return os.Getenv("CCMD_DEBUG") == "1"
}

func slog(format string, args ...any) {
	if !slogEnabled() {
		return
	}
	slogOnce.Do(func() {
		path := "/tmp/ccmd-debug.log"
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return
		}
		slogFile = f
	})
	if slogFile == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(slogFile, "%s [%d] %s\n", ts, os.Getpid(), msg)
}
