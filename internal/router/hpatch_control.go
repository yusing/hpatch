package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/yusing/mekugi/internal/shellruntime"
	"golang.org/x/term"
)

const hpatchTranslationReady = "HPATCH-READY\n"

type hpatchControlRequest struct {
	Operation string          `json:"operation"`
	Handle    string          `json:"handle"`
	Revision  uint64          `json:"revision"`
	Progress  json.RawMessage `json:"progress"`
	Source    string          `json:"source"`
}

// A bare shell starts a private, stdin-framed control channel. Neither runtime
// paths nor control payloads are command arguments. Each channel binds exactly
// one retained handle inside the inherited thread's storage, never a global
// "current script", so concurrent carriers cannot borrow each other's context.
func runHpatchControl(ctx context.Context, stdin *os.File, stdout io.Writer) error {
	directory, err := shellruntime.Directory()
	if err != nil {
		return err
	}
	path, err := shellruntime.ScriptsPath(directory, os.Getenv(shellruntime.ThreadIDEnvironment))
	if err != nil {
		return err
	}
	parent, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	root, err := openExistingShellDirectory(parent, filepath.Base(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if stdin == nil {
		return errors.New("control channel requires stdin")
	}
	if term.IsTerminal(int(stdin.Fd())) {
		state, err := term.MakeRaw(int(stdin.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(stdin.Fd()), state)
	}
	input, closeInput, err := openHpatchInput(stdin)
	if err != nil {
		return err
	}
	defer closeInput()
	// PTY stdin/stdout may share an open-file description. Making input
	// nonblocking also changes stdout; wrap a duplicate in Go's poller so a
	// large reply waits for writable capacity instead of failing with EAGAIN.
	if file, ok := stdout.(*os.File); ok {
		output, closeOutput, err := openHpatchInput(file)
		if err != nil {
			return err
		}
		defer closeOutput()
		stopOutput := context.AfterFunc(ctx, func() { _ = output.Close() })
		defer stopOutput()
		stdout = output
	}
	stop := context.AfterFunc(ctx, func() { _ = input.Close() })
	defer stop()
	// An abandoned startup expires promptly; a bound channel never outlives
	// its retained handle, even when hard cancellation skips carrier cleanup.
	if err := input.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, hpatchTranslationReady); err != nil {
		return err
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxHpatchCheckpointBytes+1)
	var bound hpatchResumeState
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var request hpatchControlRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return errors.New("invalid control frame")
		}
		if request.Operation == "close" {
			return nil
		}
		var response any
		if bound.Handle == "" {
			if request.Operation != "open" {
				return errors.New("control channel must bind a handle first")
			}
			name, err := mixedArtifactName(request.Handle)
			if err != nil {
				return err
			}
			file, err := openRegularShellFile(root, name)
			if err != nil {
				return errors.New("control handle unavailable in this thread")
			}
			err = json.NewDecoder(io.LimitReader(file, maxHpatchCheckpointBytes+1)).Decode(&bound)
			closeErr := file.Close()
			if err != nil || closeErr != nil || bound.Handle != request.Handle || !time.Now().Before(bound.ExpiresAt) {
				return errors.New("control handle invalid or expired")
			}
			if err := input.SetReadDeadline(bound.ExpiresAt); err != nil {
				return err
			}
			response = map[string]any{"opened": true}
		} else {
			if !time.Now().Before(bound.ExpiresAt) {
				return errors.New("control handle expired")
			}
			switch request.Operation {
			case "checkpoint":
				if err := runHpatchCheckpoint(ctx, root, bound.Handle, request.Revision, string(request.Progress), io.Discard); err != nil {
					return err
				}
				response = map[string]any{"revision": request.Revision + 1}
			case "translate":
				var output bytes.Buffer
				if err := runHpatchTranslation(ctx, bound.Root, request.Source, &output); err != nil {
					return err
				}
				response = json.RawMessage(bytes.TrimSpace(output.Bytes()))
			default:
				return fmt.Errorf("invalid control operation")
			}
		}
		encoded, err := json.Marshal(response)
		if err != nil || len(encoded) > maxHpatchCheckpointBytes {
			return errors.New("control response exceeds retention limit")
		}
		// Explicit acknowledgement keeps each native result well below the
		// host's output/token limits; writable PTY capacity alone is not enough.
		for len(encoded) != 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !time.Now().Before(bound.ExpiresAt) {
				return errors.New("control handle expired")
			}
			size := min(len(encoded), 16<<10)
			for !utf8.Valid(encoded[:size]) {
				size--
			}
			frame := struct {
				Data string `json:"data"`
				More bool   `json:"more"`
			}{string(encoded[:size]), size < len(encoded)}
			if file, ok := stdout.(*os.File); ok {
				deadline := time.Now().Add(time.Minute)
				if bound.ExpiresAt.Before(deadline) {
					deadline = bound.ExpiresAt
				}
				if err := file.SetWriteDeadline(deadline); err != nil {
					return err
				}
			}
			if err := json.NewEncoder(stdout).Encode(frame); err != nil {
				return err
			}
			encoded = encoded[size:]
			if frame.More {
				if !scanner.Scan() {
					return scanner.Err()
				}
				var next hpatchControlRequest
				if err := json.Unmarshal(scanner.Bytes(), &next); err != nil {
					return errors.New("invalid control acknowledgement")
				}
				if next.Operation == "close" {
					return nil
				}
				if next.Operation != "next" {
					return errors.New("control response requires acknowledgement")
				}
			}
		}
	}
	return scanner.Err()
}
