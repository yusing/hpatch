package hpatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"unicode/utf8"
)

func (w filesystemWorkspace) readFile(ctx context.Context, path string) (loadedFile, error) {
	if err := ctx.Err(); err != nil {
		return loadedFile{}, err
	}
	file, err := w.open(path)
	if err != nil {
		reason := reasonOther
		if errors.Is(err, fs.ErrNotExist) {
			reason = reasonFileMissing
		}
		return loadedFile{}, withReason(reason, fmt.Errorf("reading %s: %w", path, err))
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return loadedFile{}, fmt.Errorf("%s is not a regular file", path)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if !utf8.Valid(content) {
		return loadedFile{}, fmt.Errorf("%s is not UTF-8", path)
	}
	return loadedFile{content: string(content), mode: info.Mode()}, nil
}
