package main

import (
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// DiskIO centralizes all filesystem reads/writes.
type DiskIO interface {
	MkdirAll(path string, perm fs.FileMode) error
	ReadFile(path string) ([]byte, error)
	OpenRead(path string) (io.ReadCloser, error)
	ReadDir(path string) ([]fs.DirEntry, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	Truncate(path string, size int64) error
}

type OSDiskIO struct{}

func (OSDiskIO) MkdirAll(path string, perm fs.FileMode) error {
	slog.Debug("disk write: mkdir", "path", path, "perm", perm)
	err := os.MkdirAll(path, perm)
	if err != nil {
		slog.Debug("disk write failed: mkdir", "path", path, "error", err)
		return err
	}
	slog.Debug("disk write ok: mkdir", "path", path)
	return nil
}

func (OSDiskIO) ReadFile(path string) ([]byte, error) {
	slog.Debug("disk read: file", "path", path)
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Debug("disk read failed: file", "path", path, "error", err)
		return nil, err
	}
	slog.Debug("disk read ok: file", "path", path, "bytes", len(data))
	return data, nil
}

func (OSDiskIO) OpenRead(path string) (io.ReadCloser, error) {
	slog.Debug("disk read: open", "path", path)
	f, err := os.Open(path)
	if err != nil {
		slog.Debug("disk read failed: open", "path", path, "error", err)
		return nil, err
	}
	slog.Debug("disk read ok: open", "path", path)
	return f, nil
}

func (OSDiskIO) ReadDir(path string) ([]fs.DirEntry, error) {
	slog.Debug("disk read: dir", "path", path)
	entries, err := os.ReadDir(path)
	if err != nil {
		slog.Debug("disk read failed: dir", "path", path, "error", err)
		return nil, err
	}
	slog.Debug("disk read ok: dir", "path", path, "entries", len(entries))
	return entries, nil
}

func (OSDiskIO) WriteFile(path string, data []byte, perm fs.FileMode) error {
	slog.Debug("disk write: file", "path", path, "bytes", len(data), "perm", perm)
	recordDiskWrite(path, data)
	err := os.WriteFile(path, data, perm)
	if err != nil {
		slog.Debug("disk write failed: file", "path", path, "error", err)
		return err
	}
	slog.Debug("disk write ok: file", "path", path, "bytes", len(data))
	return nil
}

func (OSDiskIO) Truncate(path string, size int64) error {
	slog.Debug("disk write: truncate", "path", path, "size", size)
	err := os.Truncate(path, size)
	if err != nil {
		slog.Debug("disk write failed: truncate", "path", path, "error", err)
		return err
	}
	slog.Debug("disk write ok: truncate", "path", path, "size", size)
	return nil
}

func getenv(key string) string { return os.Getenv(key) }

func pid() int { return os.Getpid() }

func waitForShutdownSignal() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
