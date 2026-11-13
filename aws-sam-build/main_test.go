// Copyright 2026 Harness. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEntrypointHelper(t *testing.T) {
	if os.Getenv("SAM_MAIN_TEST_HELPER") == "1" {
		main()
	}
}

func TestEntrypointHandlesTermination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX container signals")
	}
	for _, termination := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(termination.String(), func(t *testing.T) {
			dir := t.TempDir()
			module := filepath.Join(dir, "samcli", "cli")
			if err := os.MkdirAll(module, 0700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(dir, "samcli", "__init__.py"), filepath.Join(module, "__init__.py")} {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			script := `import json, os, pathlib, time
def cli():
    pathlib.Path(os.environ["SAM_MAIN_TEST_READY"]).write_text(json.dumps({
        "password_present": "PLUGIN_PRIVATE_REGISTRY_PASSWORD" in os.environ,
        "options_present": "PLUGIN_BUILD_COMMAND_OPTIONS" in os.environ}))
    time.sleep(60)
`
			if err := os.WriteFile(filepath.Join(module, "main.py"), []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			ready := filepath.Join(dir, "ready.json")
			t.Setenv("PYTHONPATH", dir)
			t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
			t.Setenv("SAM_MAIN_TEST_HELPER", "1")
			t.Setenv("SAM_MAIN_TEST_READY", ready)
			t.Setenv("PLUGIN_PRIVATE_REGISTRY_URL", "")
			t.Setenv("PLUGIN_PRIVATE_REGISTRY_PASSWORD", "dummy-registry-secret")
			t.Setenv("PLUGIN_BUILD_COMMAND_OPTIONS", "-e TOKEN=dummy-build-secret")
			t.Setenv("PLUGIN_TEMPLATE_FILE_PATH", "template.yaml")
			t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
			cmd := exec.Command(os.Args[0], "-test.run=^TestEntrypointHelper$")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			deadline := time.After(5 * time.Second)
			for {
				if contents, err := os.ReadFile(ready); err == nil {
					var present map[string]bool
					if json.Unmarshal(contents, &present) == nil {
						if present["password_present"] || present["options_present"] {
							t.Fatal("entry point leaked consumed credential settings")
						}
						break
					}
				}
				select {
				case err := <-done:
					t.Fatalf("plugin exited before SAM started: %v: %s", err, output.String())
				case <-deadline:
					t.Fatal("SAM did not start")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err := cmd.Process.Signal(termination); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(output.String(), "error executing command") {
					t.Fatalf("plugin did not handle signal and return through Exec: %v: %s", err, output.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("signal did not cancel SAM")
			}
		})
	}
}
