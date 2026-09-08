package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestCodexArgsPreservesArguments(t *testing.T) {
	forwarded := []string{"exec", "-c", "model=\"example\"", "--", "a prompt with spaces"}
	args := codexArgs("http://127.0.0.1:12345/v1", forwarded)
	index := slices.Index(forwarded, "--")
	if !slices.Equal(args[:index], forwarded[:index]) || !slices.Equal(args[index+4:], forwarded[index:]) {
		t.Fatalf("forwarded arguments changed: %q", args)
	}
	var config struct {
		ModelProvider string `toml:"model_provider"`
		Providers     map[string]struct {
			Name    string `toml:"name"`
			BaseURL string `toml:"base_url"`
			WireAPI string `toml:"wire_api"`
			Auth    bool   `toml:"requires_openai_auth"`
		} `toml:"model_providers"`
	}
	var settings []string
	for i := index; i < index+4; i += 2 {
		if args[i] != "-c" {
			t.Fatalf("not a config override: %q", args)
		}
		settings = append(settings, args[i+1])
	}
	if _, err := toml.Decode(strings.Join(settings, "\n"), &config); err != nil {
		t.Fatal(err)
	}
	provider := config.Providers[config.ModelProvider]
	if provider.Name == "" || provider.BaseURL != "http://127.0.0.1:12345/v1" || provider.WireAPI != "responses" || !provider.Auth {
		t.Fatalf("provider = %+v", provider)
	}
	withoutDelimiter := []string{"exec", "-c", `model="example"`, "prompt"}
	if got := codexArgs("http://127.0.0.1:12345/v1", withoutDelimiter); !slices.Equal(got[:len(withoutDelimiter)], withoutDelimiter) {
		t.Fatalf("ordinary -c or prompt moved: %q", got)
	}
}

// A fake Codex process checks the real listener before exiting, without provider traffic.
func TestWrappedCodexProcess(t *testing.T) {
	if os.Getenv("HPATCH_TEST_CODEX") != "1" {
		return
	}
	interrupts := make(chan os.Signal, 1)
	if os.Getenv("HPATCH_TEST_EXIT") == "interrupt" {
		signal.Notify(interrupts, os.Interrupt)
	}
	var baseURL string
	for _, arg := range os.Args {
		start := strings.Index(arg, "http://127.0.0.1:")
		if start < 0 {
			continue
		}
		baseURL = strings.SplitN(arg[start:], "\"", 2)[0]
		break
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(strings.TrimSuffix(baseURL, "/v1") + "/api/metrics")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(90)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(91)
	}
	if err := os.WriteFile(os.Getenv("HPATCH_TEST_ADDRESS"), []byte(baseURL), 0o600); err != nil {
		os.Exit(92)
	}
	switch os.Getenv("HPATCH_TEST_EXIT") {
	case "interrupt":
		<-interrupts
		if err := os.WriteFile(os.Getenv("HPATCH_TEST_ADDRESS")+".interrupt", nil, 0o600); err != nil {
			os.Exit(94)
		}
		time.Sleep(time.Minute)
		os.Exit(95)
	case "signal":
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {}
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(93)
	default:
		code, _ := strconv.Atoi(os.Getenv("HPATCH_TEST_EXIT"))
		os.Exit(code)
	}
}

func TestWrappedRouterProcess(t *testing.T) {
	if os.Getenv("HPATCH_TEST_ROUTER") != "1" {
		return
	}
	os.Args = []string{os.Args[0], "--grok", "--model-protocol", "native", "--mentor-handoff=false", "wrap", "codex"}
	os.Exit(run())
}

func TestWrapTerminalInterruptAndTermination(t *testing.T) {
	directory := t.TempDir()
	runtimeDirectory := t.TempDir()
	addressFile := filepath.Join(directory, "address")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HPATCH_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("HPATCH_TEST_ROUTER", "1")
	t.Setenv("HPATCH_TEST_CODEX", "1")
	t.Setenv("HPATCH_TEST_EXIT", "interrupt")
	t.Setenv("HPATCH_TEST_ADDRESS", addressFile)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	stub := "#!/bin/sh\nexec " + strconv.Quote(os.Args[0]) + " -test.run=^TestWrappedCodexProcess$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "codex"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWrappedRouterProcess$")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waitFile := func(path string) []byte {
		t.Helper()
		for {
			if data, err := os.ReadFile(path); err == nil {
				return data
			}
			select {
			case err := <-done:
				t.Fatalf("wrapper exited early: %v", err)
			case <-ctx.Done():
				t.Fatal("wrapper timed out")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	baseURL := string(waitFile(addressFile))
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitFile(addressFile + ".interrupt")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(strings.TrimSuffix(baseURL, "/v1") + "/api/metrics")
	if err != nil {
		t.Fatalf("router stopped on terminal interrupt: %v", err)
	}
	response.Body.Close()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || cmd.ProcessState.ExitCode() != 143 {
		t.Fatalf("termination = %v, %v", cmd.ProcessState, err)
	}
	if !strings.Contains(logs.String(), "grok_subagents=true") || !strings.Contains(logs.String(), "model_protocol=native") || !strings.Contains(logs.String(), "mentor_handoff=false") {
		t.Fatalf("router flags did not reach wrapped server: %s", logs.String())
	}
	entries, err := os.ReadDir(runtimeDirectory)
	if err != nil || len(entries) != 0 {
		t.Errorf("runtime resources survived: %v, %v", entries, err)
	}
}

func TestWrapCodexLifecycle(t *testing.T) {
	for _, test := range []struct {
		name, exit string
		code       int
	}{
		{"success", "0", 0}, {"failure", "23", 23}, {"signal", "signal", 143}, {"termination", "wait", 143},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			runtimeDirectory := t.TempDir()
			addressFile := filepath.Join(directory, "address")
			t.Setenv("CODEX_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("HPATCH_RUNTIME_DIR", runtimeDirectory)
			t.Setenv("HPATCH_TEST_CODEX", "1")
			t.Setenv("HPATCH_TEST_EXIT", test.exit)
			t.Setenv("HPATCH_TEST_ADDRESS", addressFile)
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			stub := "#!/bin/sh\nexec " + strconv.Quote(os.Args[0]) + " -test.run=^TestWrappedCodexProcess$ -- \"$@\"\n"
			if err := os.WriteFile(filepath.Join(directory, "codex"), []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.exit == "wait" {
				go func() {
					for {
						if _, err := os.Stat(addressFile); err == nil {
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
				}()
			}
			deadline := time.AfterFunc(20*time.Second, cancel)
			defer deadline.Stop()
			code, err := wrapCodex(ctx, nil, []string{"exec", "prompt with spaces"})
			if err != nil || code != test.code {
				t.Fatalf("wrap = %d, %v; want %d", code, err, test.code)
			}
			address, err := os.ReadFile(addressFile)
			if err != nil {
				t.Fatal(err)
			}
			host := strings.TrimSuffix(strings.TrimPrefix(string(address), "http://"), "/v1")
			conn, err := net.DialTimeout("tcp", host, time.Second)
			if err == nil {
				conn.Close()
				t.Error("listener survived Codex exit")
			}
			entries, err := os.ReadDir(runtimeDirectory)
			if err != nil || len(entries) != 0 {
				t.Errorf("runtime resources survived: %v, %v", entries, err)
			}
		})
	}
}

func TestWrapCodexMissingExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, err := wrapCodex(t.Context(), nil, nil)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "locate codex") {
		t.Fatalf("wrap = %d, %v", code, err)
	}
}

func TestValidateCodexArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--oss"}, {"exec", "--local-provider", "ollama"}, {"--local-provider=ollama"},
		{"-c", `model_provider="other"`}, {"--config=model_providers.other={}"},
		{`-cmodel_provider="other"`}, {`-c=model_provider="other"`},
		{"--config", `"model_providers".hpatch_wrap.base_url="https://example.com"`},
		{"-c", `openai_base_url="https://example.com"`}, {"-c", `oss_provider="ollama"`},
	} {
		if err := validateCodexArgs(args); err == nil {
			t.Errorf("accepted provider override: %q", args)
		}
	}
	for _, args := range [][]string{
		nil, {"exec", "a prompt with spaces"}, {"--profile", "work"},
		{"exec", "-c", `model="example"`}, {"--", "--oss"},
		{"--", `-cmodel_provider="other"`}, {"-c"},
	} {
		if err := validateCodexArgs(args); err != nil {
			t.Errorf("rejected ordinary arguments %q: %v", args, err)
		}
	}
}

func TestRunWrapUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}} {
		if code := runWrap(nil, args); code != 2 {
			t.Errorf("runWrap(%q) = %d", args, code)
		}
	}
}

func TestWrapCodexStartupFailures(t *testing.T) {
	for _, failure := range []string{"router", "codex"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			runtimeDirectory := t.TempDir()
			configDirectory := t.TempDir()
			t.Setenv("CODEX_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", configDirectory)
			t.Setenv("HPATCH_RUNTIME_DIR", runtimeDirectory)
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			marker := filepath.Join(directory, "launched")
			stub := "#!/bin/sh\ntouch " + strconv.Quote(marker) + "\n"
			if failure == "codex" {
				stub = "#!/nonexistent-hpatch-test-interpreter\n"
			} else {
				userConfig, err := os.UserConfigDir()
				if err != nil {
					t.Fatal(err)
				}
				plugins := filepath.Join(userConfig, "hpatch", "plugins")
				if err := os.MkdirAll(plugins, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(plugins, "broken.mjs"), []byte("this is not valid JavaScript"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(directory, "codex"), []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			code, err := wrapCodex(ctx, nil, nil)
			if code != 1 || err == nil {
				t.Fatalf("wrap = %d, %v", code, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("Codex launched despite startup failure: %v", err)
			}
			entries, err := os.ReadDir(runtimeDirectory)
			if err != nil || len(entries) != 0 {
				t.Errorf("runtime resources survived: %v, %v", entries, err)
			}
		})
	}
}
