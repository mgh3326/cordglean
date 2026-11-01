package main

import (
	"errors"
	"os"
	"path/filepath"
)

type secretFileError struct{ message string }

func (e *secretFileError) Error() string { return e.message }

type secretFile struct {
	path      string
	staging   string
	file      *os.File
	info      os.FileInfo
	overwrite bool
	committed bool
}

func prepareSecretFile(path string, overwrite bool) (*secretFile, error) {
	if path == "" {
		return nil, errors.New("output_path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, &secretFileError{message: "output_path could not be prepared"}
	}
	dest := &secretFile{path: abs, overwrite: overwrite}
	if overwrite {
		info, err := os.Lstat(abs)
		if err == nil && !info.Mode().IsRegular() {
			return nil, errors.New("output_path must be a regular file, not a symlink or directory")
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, &secretFileError{message: "output_path could not be inspected"}
		}
		dest.file, err = os.CreateTemp(filepath.Dir(abs), ".cordglean-webhook-*")
		if err == nil {
			dest.staging = dest.file.Name()
		}
	} else {
		dest.staging = abs
		dest.file, err = os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if err != nil {
		if os.IsExist(err) {
			return nil, errors.New("output_path already exists; set overwrite=true to replace")
		}
		return nil, &secretFileError{message: "output_path could not be opened"}
	}
	dest.info, err = dest.file.Stat()
	if err != nil || dest.file.Chmod(0600) != nil {
		dest.abort()
		return nil, &secretFileError{message: "output_path permissions could not be secured"}
	}
	return dest, nil
}

func (d *secretFile) commit(secret string) error {
	if _, err := d.file.WriteString(secret); err != nil {
		return &secretFileError{message: "webhook secret could not be saved"}
	}
	if err := d.file.Sync(); err != nil {
		return &secretFileError{message: "webhook secret could not be saved"}
	}
	if err := d.file.Close(); err != nil {
		return &secretFileError{message: "webhook secret could not be saved"}
	}
	if info, err := os.Lstat(d.staging); err != nil || !os.SameFile(info, d.info) {
		return &secretFileError{message: "output_path changed while the webhook was being created"}
	}
	if d.overwrite {
		if err := os.Rename(d.staging, d.path); err != nil {
			return &secretFileError{message: "webhook secret could not be saved"}
		}
	}
	d.committed = true
	return nil
}

func (d *secretFile) abort() {
	if d.file != nil {
		d.file.Close()
	}
	if !d.committed && d.staging != "" {
		if info, err := os.Lstat(d.staging); err == nil && d.info != nil && os.SameFile(info, d.info) {
			os.Remove(d.staging)
		}
	}
}
