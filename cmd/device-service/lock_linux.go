//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// processLock is one exclusive kernel lock over the fixed runtime directory.
// It is held for the whole process lifetime; the kernel releases it on exit.
type processLock struct {
	file *os.File
}

func acquireProcessLock(dir string) (*processLock, error) {
	path := filepath.Join(dir, "device-service.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open process lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another device service instance owns %s", path)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	}
	return &processLock{file: file}, nil
}

func (l *processLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
