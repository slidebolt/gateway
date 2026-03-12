package main

import (
	"io"
	"io/fs"
	"log"
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
	log.Printf("disk write: mkdir path=%s perm=%#o", path, perm)
	err := os.MkdirAll(path, perm)
	if err != nil {
		log.Printf("disk write failed: mkdir path=%s err=%v", path, err)
		return err
	}
	log.Printf("disk write ok: mkdir path=%s", path)
	return nil
}

func (OSDiskIO) ReadFile(path string) ([]byte, error) {
	log.Printf("disk read: file path=%s", path)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("disk read failed: file path=%s err=%v", path, err)
		return nil, err
	}
	log.Printf("disk read ok: file path=%s bytes=%d", path, len(data))
	return data, nil
}

func (OSDiskIO) OpenRead(path string) (io.ReadCloser, error) {
	log.Printf("disk read: open path=%s", path)
	f, err := os.Open(path)
	if err != nil {
		log.Printf("disk read failed: open path=%s err=%v", path, err)
		return nil, err
	}
	log.Printf("disk read ok: open path=%s", path)
	return f, nil
}

func (OSDiskIO) ReadDir(path string) ([]fs.DirEntry, error) {
	log.Printf("disk read: dir path=%s", path)
	entries, err := os.ReadDir(path)
	if err != nil {
		log.Printf("disk read failed: dir path=%s err=%v", path, err)
		return nil, err
	}
	log.Printf("disk read ok: dir path=%s entries=%d", path, len(entries))
	return entries, nil
}

func (OSDiskIO) WriteFile(path string, data []byte, perm fs.FileMode) error {
	log.Printf("disk write: file path=%s bytes=%d perm=%#o", path, len(data), perm)
	recordDiskWrite(path, data)
	err := os.WriteFile(path, data, perm)
	if err != nil {
		log.Printf("disk write failed: file path=%s err=%v", path, err)
		return err
	}
	log.Printf("disk write ok: file path=%s bytes=%d", path, len(data))
	return nil
}

func (OSDiskIO) Truncate(path string, size int64) error {
	log.Printf("disk write: truncate path=%s size=%d", path, size)
	err := os.Truncate(path, size)
	if err != nil {
		log.Printf("disk write failed: truncate path=%s err=%v", path, err)
		return err
	}
	log.Printf("disk write ok: truncate path=%s size=%d", path, size)
	return nil
}

func getenv(key string) string { return os.Getenv(key) }

func pid() int { return os.Getpid() }

func waitForShutdownSignal() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
