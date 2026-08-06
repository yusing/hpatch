package hpatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"unicode/utf8"
)

func (w filesystemWorkspace) openRegularFile(ctx context.Context, path string) (*os.File, fs.FileMode, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	file, err := w.root.Open(path)
	if err != nil {
		reason := reasonOther
		if errors.Is(err, fs.ErrNotExist) {
			reason = reasonFileMissing
		}
		return nil, 0, withReason(reason, fmt.Errorf("reading %s: %w", path, err))
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	return file, info.Mode(), nil
}

func (w filesystemWorkspace) readFile(ctx context.Context, path string) (loadedFile, error) {
	file, mode, err := w.openRegularFile(ctx, path)
	if err != nil {
		return loadedFile{}, err
	}
	defer func() {
		_ = file.Close()
	}()

	content, err := io.ReadAll(file)
	if err != nil {
		return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if !utf8.Valid(content) {
		return loadedFile{}, fmt.Errorf("%s is not UTF-8", path)
	}
	return loadedFile{content: string(content), mode: mode}, nil
}
