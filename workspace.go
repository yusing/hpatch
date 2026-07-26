package hpatch

import (
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
	originalPath string
	path         string
	original     string
	mode         fs.FileMode
	created      bool
	deleted      bool
	editor       editor
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

type workspace struct {
	paths   map[string]*fileState
	blocked map[string]bool
	files   []*fileState
	active  *fileState
	load    fileLoader
	exists  pathProbe
}

func (p *program) evaluate(resolve pathResolver, load fileLoader, exists pathProbe) ([]change, commandMetrics, string, error) {
	w := &workspace{paths: make(map[string]*fileState), blocked: make(map[string]bool), load: load, exists: exists}
	var commands commandMetrics
	for commandIndex, command := range p.instructions {
		commands.invoke(command.operation)
		diagnosticPath := command.path
		if command.path != "" {
			resolved, err := resolve(command.path)
			if err != nil {
				commands.fail(command.operation)
				return nil, commands, "", &commandError{Command: commandIndex + 1, Line: command.line, Operation: command.operation, Path: diagnosticPath, Category: commandCategory(command.operation), Message: err.Error()}
			}
			command.path = resolved
		}
		if err := w.execute(command, commandIndex+1); err != nil {
			commands.fail(command.operation)
			return nil, commands, "", &commandError{Command: commandIndex + 1, Line: command.line, Operation: command.operation, Path: w.diagnosticPath(command), Category: commandCategory(command.operation), Message: err.Error()}
		}
	}
	return w.changes(), commands, w.finalStateReport(), nil
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
	case "sel", "tsel", "rsel", "bsel", "bsel_next":
		return "selection"
	case "type", "del", "dup":
		return "edit"
	default:
		panic("parsed instruction has no diagnostic category: " + operation)
	}
}

func (w *workspace) execute(command instruction, commandIndex int) error {
	switch command.operation {
	case "in":
		return w.selectFile(command.path)
	case "new":
		return w.newFile(command.path)
	case "mv":
		return w.moveFile(command.path)
	case "rm":
		return w.removeFile()
	}
	if w.active == nil {
		return fmt.Errorf("%s requires an active file", command.operation)
	}

	origin := editOrigin{command: commandIndex, line: command.line, operation: command.operation}
	switch command.operation {
	case "sel":
		return w.active.editor.selectColumns(command.lineRef, command.start, command.end)
	case "tsel":
		return w.active.editor.selectOccurrence(command.lineRef, command.occurrence, command.count, command.text)
	case "bsel":
		return w.active.editor.selectBlock(command.text, command.endText)
	case "bsel_next":
		return w.active.editor.selectNextBlock(command.text, command.endText)
	case "rsel":
		return w.active.editor.selectLines(command.lineRef, command.endLineRef)
	case "type":
		return w.active.editor.typeText(command.text, origin)
	case "del":
		return w.active.editor.deleteSelection(origin)
	case "dup":
		return w.active.editor.duplicateSelection(origin)
	default:
		panic("parsed instruction has no executor: " + command.operation)
	}
}

func (w *workspace) selectFile(path string) error {
	if file := w.paths[path]; file != nil {
		w.active = file
		file.editor.resetCursor()
		return nil
	}
	if w.blocked[path] {
		return fmt.Errorf("%s does not exist in the pending workspace", path)
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

func (w *workspace) newFile(path string) error {
	if err := w.validateFreeDestination(path); err != nil {
		return err
	}
	file := &fileState{path: path, created: true}
	w.paths[path] = file
	w.files = append(w.files, file)
	w.active = file
	return nil
}

func (w *workspace) moveFile(path string) error {
	if w.active == nil {
		return fmt.Errorf("mv requires an active file")
	}
	if err := w.validateFreeDestination(path); err != nil {
		return err
	}
	oldPath := w.active.path
	delete(w.paths, oldPath)
	if !w.active.created && oldPath == w.active.originalPath {
		w.blocked[oldPath] = true
	}
	w.active.path = path
	w.paths[path] = w.active
	return nil
}

func (w *workspace) removeFile() error {
	if w.active == nil {
		return fmt.Errorf("rm requires an active file")
	}
	file := w.active
	if !file.created {
		if edit, ok := file.editor.firstEdit(); ok {
			return fmt.Errorf(
				"cannot remove a baseline file after content edit from command %d (source line %d, operation %q)",
				edit.command,
				edit.line,
				edit.operation,
			)
		}
	}
	delete(w.paths, file.path)
	if !file.created {
		w.blocked[file.originalPath] = true
	}
	file.deleted = true
	w.active = nil
	return nil
}

func (w *workspace) pathOccupied(path string) (bool, error) {
	if w.paths[path] != nil || w.blocked[path] {
		return true, nil
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
		return fmt.Errorf("parent directory %s does not exist", parent)
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
		return fmt.Errorf("destination %s already exists", path)
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
