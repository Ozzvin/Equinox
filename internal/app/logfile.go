package app

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// rotatingLog is a log file that never grows past max bytes: when it would, the current
// file becomes "<path>.old" (replacing the previous one) and a new file is started.
type rotatingLog struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func openRotating(path string, max int64) (*rotatingLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &rotatingLog{path: path, max: max}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *rotatingLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, fi.Size()
	return nil
}

func (l *rotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		l.f.Close()
		_ = os.Remove(l.path + ".old")
		_ = os.Rename(l.path, l.path+".old")
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// SetupLog sends the standard logger and everything written to os.Stderr into a size-capped
// log file. A GUI program has no console, so this is where its messages end up. Both go
// through one writer because Windows will not rename a file that is open twice.
func SetupLog(path string, max int64) error {
	l, err := openRotating(path, max)
	if err != nil {
		return err
	}
	log.SetOutput(l)
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	os.Stderr = pw
	go func() { _, _ = io.Copy(l, pr) }()
	return nil
}
