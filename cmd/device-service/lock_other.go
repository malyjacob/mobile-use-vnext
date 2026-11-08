//go:build !linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// processLock is a development-host fallback. It is not evidence for the phone
// runtime durability/lock semantics; the production target is linux/arm64.
type processLock struct {
	file *os.File
	path string
}

func acquireProcessLock(dir string) (*processLock, error) {
	path := filepath.Join(dir, "device-service.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("another device service instance owns %s", path)
	}
	_, _ = file.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	return &processLock{file: file, path: path}, nil
}

func (l *processLock) Release() {
	if l == nil {
		return
	}
	if l.file != nil {
		_ = l.file.Close()
	}
	_ = os.Remove(l.path)
}
