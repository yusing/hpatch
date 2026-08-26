package toolplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func invoke(
	ctx context.Context,
	node, hostPath, isolatedCWD, processDirectory string,
	processEnvironment []string,
	outputLimit int64,
	inheritedInput *os.File,
	transientExtraFiles []*os.File,
	request, response any,
) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode plugin runtime request: %w", err)
	}
	command := exec.CommandContext(ctx, node, hostPath)
	ConfigureProcessGroup(command)
	if inheritedInput != nil {
		command.ExtraFiles = append([]*os.File{inheritedInput}, transientExtraFiles...)
	}
	if isolatedCWD != "" {
		command.Dir = isolatedCWD
		command.Env = []string{
			"HOME=" + filepath.Dir(isolatedCWD),
			"NODE_NO_WARNINGS=1",
			"PATH=" + filepath.Dir(node),
		}
	} else {
		command.Dir = processDirectory
		command.Env = processEnvironment
	}
	command.Stdin = bytes.NewReader(encoded)
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("capture plugin runtime output: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return fmt.Errorf("capture plugin runtime diagnostics: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start plugin runtime: %w", err)
	}
	var closeErr error
	for _, file := range transientExtraFiles {
		closeErr = errors.Join(closeErr, file.Close())
	}
	if closeErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("close inherited plugin runtime files: %w", closeErr)
	}

	type capturedOutput struct {
		data     []byte
		overflow bool
		err      error
	}
	capture := func(reader io.Reader) <-chan capturedOutput {
		result := make(chan capturedOutput, 1)
		go func() {
			var output bytes.Buffer
			_, readErr := io.CopyN(&output, reader, outputLimit+1)
			switch {
			case readErr == nil:
				_, drainErr := io.Copy(io.Discard, reader)
				result <- capturedOutput{
					data:     bytes.Clone(output.Bytes()[:outputLimit]),
					overflow: true,
					err:      drainErr,
				}
			case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
				result <- capturedOutput{data: output.Bytes()}
			default:
				result <- capturedOutput{data: output.Bytes(), err: readErr}
			}
		}()
		return result
	}
	stdoutResult := capture(stdoutPipe)
	stderrResult := capture(stderrPipe)
	stdout := <-stdoutResult
	stderr := <-stderrResult
	runErr := command.Wait()

	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if stdout.err != nil || stderr.err != nil {
		return fmt.Errorf("read plugin runtime output: %w", errors.Join(stdout.err, stderr.err))
	}
	if stdout.overflow || stderr.overflow {
		return fmt.Errorf("plugin runtime output exceeds %d bytes", outputLimit)
	}
	if runErr != nil {
		diagnostic := strings.TrimSpace(string(stderr.data))
		if diagnostic == "" {
			return fmt.Errorf("invoke plugin runtime: %w", runErr)
		}
		return fmt.Errorf("invoke plugin runtime: %w: %s", runErr, diagnostic)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(response); err != nil {
		return fmt.Errorf("decode plugin runtime result: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("plugin runtime returned trailing output")
	}
	return nil
}
