package hpatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

type loadedFile struct {
	content string
	mode    fs.FileMode
}

type (
	fileLoader   func(path string) (loadedFile, error)
	pathProbe    func(path string) (fs.FileMode, bool, error)
	pathResolver func(path string) (string, error)
)

type fileState struct {
	originalPath   string
	path           string
	original       string
	mode           fs.FileMode
	created        bool
	deleted        bool
	mutationOrigin editOrigin
	editor         editor
}

type changeKind int

const (
	changeAdd changeKind = iota
	changeUpdate
	changeDelete
)

type change struct {
	kind         changeKind
	originalPath string
	path         string
	original     string
	content      string
	mode         fs.FileMode
}

type clipboardContent struct {
	text     string
	linewise bool
}

type workspace struct {
	paths         map[string]*fileState
	blocked       map[string]bool
	reserved      map[string]bool
	removed       map[string]*fileState
	files         []*fileState
	active        *fileState
	reportedEdits []*reportedEdit
	clipboard     *clipboardContent
	load          fileLoader
	exists        pathProbe
}

func (p *program) evaluate(ctx context.Context, resolve pathResolver, load fileLoader, exists pathProbe) ([]change, invocationMetrics, string, error) {
	w := &workspace{
		paths:    make(map[string]*fileState),
		blocked:  make(map[string]bool),
		reserved: make(map[string]bool),
		removed:  make(map[string]*fileState),
		load:     load,
		exists:   exists,
	}
	var events invocationMetrics
	for commandIndex, command := range p.instructions {
		if err := ctx.Err(); err != nil {
			return nil, events, "", err
		}
		events.invoke(command.operation, command.attempt)
		diagnosticPath := command.path
		if command.path != "" {
			resolved, err := resolve(command.path)
			if err != nil {
				reason := reasonPath
				events.fail(command.operation, command.attempt, reason)
				return nil, events, "", &commandError{Attempt: command.attempt, Reason: reason, Command: commandIndex + 1, Line: command.line, Operation: command.operation, Path: diagnosticPath, Category: commandCategory(command.operation), Source: command.source, Message: err.Error()}
			}
			command.path = resolved
		}
		if err := w.execute(command, commandIndex+1); err != nil {
			reason := reasonOf(err, reasonOther)
			events.fail(command.operation, command.attempt, reason)
			failure := &commandError{Attempt: command.attempt, Reason: reason, Command: commandIndex + 1, Line: command.line, Operation: command.operation, Path: w.diagnosticPath(command), Category: commandCategory(command.operation), Source: command.source, Message: err.Error(), Repair: w.repairContext(command, reason)}
			if correction, ok := errors.AsType[*indentationCorrectionError](err); ok {
				failure.Reason = reasonEditConflict
				failure.Message = "indentation-only change to preserved text"
				failure.Repair = correction.diagnostic()
				failure.Correction = correctedTypeCommand(command, correction.correctedText)
			}
			return nil, events, "", failure
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, events, "", err
	}
	if failure := w.formatGoFiles(); failure != nil {
		if failure.Operation != "" {
			events.fail(failure.Operation, failure.Attempt, failure.Reason)
		}
		return nil, events, "", failure
	}
	changes := w.changes()

	return changes, events, w.finalStateReport(changes), nil
}

func (w *workspace) diagnosticPath(command instruction) string {
	if command.path != "" {
		return command.path
	}
	if w.active != nil {
		return w.active.path
	}
	return ""
}

func commandCategory(operation string) string {
	switch operation {
	case "in", "new", "mv", "rm":
		return "file"
	case "tsel", "rsel":
		return "selection"
	case "type", "del", "copy", "cut", "paste":
		return "edit"
	case "commit":
		return "state"
	default:
		panic("parsed instruction has no diagnostic category: " + operation)
	}
}

func (w *workspace) execute(command instruction, commandIndex int) error {
	origin := editOrigin{command: commandIndex, line: command.line, operation: command.operation}
	switch command.operation {
	case "in":
		return w.selectFile(command.path)
	case "new":
		return w.newFile(command.path, origin)
	case "mv":
		return w.moveFile(command.path, origin)
	case "rm":
		return w.removeFile()
	case "commit":
		w.commitGeneration()
		return nil
	}
	if w.active == nil {
		return withReason(reasonActiveFile, fmt.Errorf("%s requires an active file", command.operation))
	}

	file := w.active
	textMatches := len(file.editor.selections) != 0 && file.editor.textMatches
	var err error
	switch command.operation {
	case "tsel":
		return w.active.editor.selectMatches(command.lineHash, command.count, command.text)

	case "rsel":
		return w.active.editor.selectLines(command.lineHash, command.endHash)
	case "type":
		err = file.editor.typeText(command.text, origin)
	case "del":
		err = file.editor.deleteSelection(origin)
	case "copy", "cut":
		clipboard, ok := file.editor.selectedClipboard()
		if !ok {
			return withReason(reasonSelectionRequired, fmt.Errorf("%s requires a selection", command.operation))
		}
		if command.operation == "cut" {
			if err := file.editor.deleteSelection(origin); err != nil {
				return err
			}
		}
		w.clipboard = &clipboard
		if command.operation == "copy" {
			return nil
		}
	case "paste":
		if w.clipboard == nil {
			return withReason(reasonClipboardEmpty, fmt.Errorf("paste requires a preceding copy or cut in the same script"))
		}
		err = file.editor.pasteClipboard(*w.clipboard, origin)
	default:
		panic("parsed instruction has no executor: " + command.operation)
	}
	if err != nil {
		return err
	}
	if edit := file.editor.reportedEdit(origin, textMatches); edit != nil {
		edit.file = file
		w.reportedEdits = append(w.reportedEdits, edit)
	}
	return nil
}

func (w *workspace) commitGeneration() {
	for _, file := range w.files {
		if !file.deleted {
			file.editor.commitGeneration()
		}
	}
	clear(w.reserved)
}

func (w *workspace) selectFile(path string) error {
	if file := w.paths[path]; file != nil {
		w.active = file
		file.editor.resetCursor()
		return nil
	}
	if w.blocked[path] {
		return withReason(reasonFileMissing, fmt.Errorf("%s does not exist in the pending workspace", path))
	}
	loaded, err := w.load(path)
	if err != nil {
		return err
	}
	file := &fileState{
		originalPath: path,
		path:         path,
		original:     loaded.content,
		mode:         loaded.mode,
		editor:       editor{baseline: loaded.content},
	}
	w.paths[path] = file
	w.files = append(w.files, file)
	w.active = file
	return nil
}

func (w *workspace) newFile(path string, origin editOrigin) error {
	if err := w.validateFreeDestination(path); err != nil {
		return err
	}
	if file := w.removed[path]; file != nil {
		delete(w.removed, path)
		file.path = path
		file.deleted = false
		file.mutationOrigin = origin
		file.editor = editor{}
		w.paths[path] = file
		w.active = file
		return nil
	}
	file := &fileState{path: path, created: true, mutationOrigin: origin}
	w.paths[path] = file
	w.files = append(w.files, file)
	w.active = file
	return nil
}

func (w *workspace) moveFile(path string, origin editOrigin) error {
	if w.active == nil {
		return withReason(reasonActiveFile, fmt.Errorf("mv requires an active file"))
	}
	if err := w.validateFreeDestination(path); err != nil {
		return err
	}
	oldPath := w.active.path
	delete(w.paths, oldPath)
	w.blocked[oldPath] = true
	w.reserved[oldPath] = true
	w.active.path = path
	w.active.mutationOrigin = origin
	w.paths[path] = w.active
	return nil
}

func (w *workspace) removeFile() error {
	if w.active == nil {
		return withReason(reasonActiveFile, fmt.Errorf("rm requires an active file"))
	}
	file := w.active
	if !file.created {
		if edit, ok := file.editor.firstEdit(); ok {
			return withReason(reasonEditConflict, fmt.Errorf(
				"cannot remove a baseline file after content edit from command %d (source line %d, operation %q)",
				edit.command,
				edit.line,
				edit.operation,
			))
		}
	}
	removedPath := file.path
	delete(w.paths, removedPath)
	w.blocked[removedPath] = true
	w.reserved[removedPath] = true
	w.removed[removedPath] = file
	if !file.created {
		w.blocked[file.originalPath] = true
	}
	file.deleted = true
	retained := w.reportedEdits[:0]
	for _, edit := range w.reportedEdits {
		if edit.file != file {
			retained = append(retained, edit)
		}
	}
	w.reportedEdits = retained
	w.active = nil
	return nil
}

func (w *workspace) pathOccupied(path string) (bool, error) {
	if w.paths[path] != nil || w.reserved[path] {
		return true, nil
	}
	if w.blocked[path] {
		return false, nil
	}
	_, occupied, err := w.exists(path)
	if err != nil {
		return false, fmt.Errorf("checking destination %s: %w", path, err)
	}
	return occupied, nil
}

func (w *workspace) validateDestinationParent(path string) error {
	parent := filepath.Dir(path)
	mode, exists, err := w.exists(parent)
	if err != nil {
		return fmt.Errorf("checking parent directory %s: %w", parent, err)
	}
	if !exists || !mode.IsDir() {
		return withReason(reasonPath, fmt.Errorf("parent directory %s does not exist", parent))
	}
	return nil
}

func (w *workspace) validateFreeDestination(path string) error {
	if err := w.validateDestinationParent(path); err != nil {
		return err
	}
	occupied, err := w.pathOccupied(path)
	if err != nil {
		return err
	}
	if occupied {
		return withReason(reasonFileConflict, fmt.Errorf("destination %s already exists", path))
	}
	return nil
}

func (w *workspace) changes() []change {
	changes := make([]change, 0, len(w.files))
	for _, file := range w.files {
		content := file.editor.content()
		switch {
		case file.created && file.deleted:
			continue
		case file.created:
			changes = append(changes, change{kind: changeAdd, path: file.path, content: content, mode: 0o644})
		case file.deleted:
			changes = append(changes, change{
				kind:         changeDelete,
				originalPath: file.originalPath,
				original:     file.original,
				mode:         file.mode,
			})
		case file.originalPath != file.path || file.original != content:
			changes = append(changes, change{
				kind:         changeUpdate,
				originalPath: file.originalPath,
				path:         file.path,
				original:     file.original,
				content:      content,
				mode:         file.mode,
			})
		}
	}
	return changes
}
