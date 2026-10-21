// Package applog writes a rotating debug log to the user's config folder:
// Windows: %APPDATA%\TSG Embroidery Pro\logs\debug.log
package applog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxSize = 5 << 20 // rotate at 5 MB

var (
	mu     sync.Mutex
	logger = log.New(io.Discard, "", log.LstdFlags|log.Lmicroseconds)
	path   string
)

// Init opens the log file under dir/logs. Safe to call once at startup.
func Init(dir string) string {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return ""
	}
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return ""
	}
	path = filepath.Join(logs, "debug.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		path = ""
		return ""
	}
	logger.SetOutput(f)
	return path
}

// Path returns the active log file ("" if logging is off).
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

func write(level, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	msg = strings.ReplaceAll(msg, "\n", "\n    ")
	logger.Printf("%-5s %s", level, msg)
}

func Infof(format string, a ...any)  { write("INFO", format, a...) }
func Errorf(format string, a ...any) { write("ERROR", format, a...) }
