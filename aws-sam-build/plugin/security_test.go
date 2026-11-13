// Copyright 2026 Harness. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type invocation struct {
	Args        []string `json:"args"`
	OSArgs      []string `json:"os_args"`
	Stdin       string   `json:"stdin"`
	Environment []string `json:"environment"`
}

func TestMain(m *testing.M) {
	if os.Getenv("SAM_PLUGIN_TEST_HELPER") != "1" || filepath.Base(os.Args[0]) != "docker" {
		os.Exit(m.Run())
	}
	input, _ := io.ReadAll(os.Stdin)
	record := invocation{Args: os.Args[1:], Stdin: string(input), Environment: os.Environ()}
	encoded, _ := json.Marshal(record)
	if os.WriteFile(filepath.Join(os.Getenv("SAM_PLUGIN_TEST_CAPTURE"), "docker.json"), encoded, 0600) != nil {
		os.Exit(22)
	}
	if os.Getenv("SAM_PLUGIN_TEST_SLEEP") == "docker" {
		time.Sleep(time.Minute)
	}
	fmt.Print(os.Getenv("SAM_PLUGIN_TEST_OUTPUT_DOCKER"))
	code, _ := strconv.Atoi(os.Getenv("SAM_PLUGIN_TEST_EXIT_DOCKER"))
	os.Exit(code)
}

// Execute the real Python bridge with an instrumented SAM console entry point.
// This fixture records transport; it deliberately does not imitate Click or SAM's
// config parsers. Those must receive the original arguments without conversion.
const captureSAM = `import json, os, pathlib, sys, time

def cli():
    capture = pathlib.Path(os.environ["SAM_PLUGIN_TEST_CAPTURE"])
    record = {"args": sys.argv[1:], "os_args": sys.orig_argv[1:],
              "stdin": sys.stdin.read(), "environment": [k+"="+v for k,v in os.environ.items()]}
    (capture / "sam.json").write_text(json.dumps(record))
    # Model a native save BEFORE a build failure, rather than assuming success
    # is a prerequisite for saving. The bridge must not undo or rewrite it.
    save = os.environ.get("SAM_PLUGIN_TEST_NATIVE_SAVE")
    if save:
        with open(save, "a") as output:
            output.write("# native-save-before-build\n")
    if os.environ.get("SAM_PLUGIN_TEST_SLEEP") == "sam":
        time.sleep(60)
    sys.exit(int(os.environ.get("SAM_PLUGIN_TEST_EXIT_SAM", "0")))
`

func helperCommands(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("python3 is required for SAM entry-point tests")
	}
	dir, tmp := t.TempDir(), t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, filepath.Join(dir, "docker")); err != nil {
		t.Fatal(err)
	}
	fixture(t, filepath.Join(dir, "samcli", "__init__.py"), "")
	fixture(t, filepath.Join(dir, "samcli", "cli", "__init__.py"), "")
	fixture(t, filepath.Join(dir, "samcli", "cli", "main.py"), captureSAM)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PYTHONPATH", dir)
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	t.Setenv("TMPDIR", tmp)
	t.Setenv("SAM_PLUGIN_TEST_HELPER", "1")
	t.Setenv("SAM_PLUGIN_TEST_CAPTURE", dir)
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	return dir, tmp
}

func fixture(t *testing.T, path, contents string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func readInvocation(t *testing.T, dir, command string) invocation {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(dir, command+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var result invocation
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func requireCleanTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected temporary files: %v", entries)
	}
}

func TestExecKeepsCredentialsOutOfOSArguments(t *testing.T) {
	dir, tmp := helperCommands(t)
	password := "dummy-registry-'$()"
	options := `-e TOKEN=dummy-build-token -e GRADLE_OPTS="-Duser=gitlab -Dpassword=dummy-gradle-token" --parallel`
	t.Setenv("PLUGIN_PRIVATE_REGISTRY_PASSWORD", password)
	t.Setenv("PLUGIN_BUILD_COMMAND_OPTIONS", options)
	t.Setenv("CUSTOM_BUILD_ENV", "keep-me")
	if err := Exec(context.Background(), Args{PrivateRegistryURL: "example.invalid", PrivateRegistryUsername: "gitlab", PrivateRegistryPassword: password, TemplateFilePath: "template.yaml", BuildImage: "example.invalid/build:dummy", BuildCommandOptions: options}); err != nil {
		t.Fatal(err)
	}
	docker := readInvocation(t, dir, "docker")
	if !reflect.DeepEqual(docker.Args, []string{"login", "example.invalid", "--username", "gitlab", "--password-stdin"}) || docker.Stdin != password {
		t.Fatalf("unexpected login transport: %#v", docker)
	}
	sam := readInvocation(t, dir, "sam")
	want := []string{"build", "--use-container", "--build-image", "example.invalid/build:dummy", "--template-file", "template.yaml", "-e", "TOKEN=dummy-build-token", "-e", "GRADLE_OPTS=-Duser=gitlab -Dpassword=dummy-gradle-token", "--parallel"}
	if !reflect.DeepEqual(sam.Args, want) || sam.Stdin != "" {
		t.Fatalf("SAM input changed: %#v", sam)
	}
	for _, osArgs := range [][]string{docker.Args, sam.OSArgs} {
		for _, secret := range []string{password, "dummy-build-token", "dummy-gradle-token"} {
			if strings.Contains(strings.Join(osArgs, " "), secret) {
				t.Fatalf("credential in OS argv: %q", osArgs)
			}
		}
	}
	for _, command := range []invocation{docker, sam} {
		for _, entry := range command.Environment {
			if strings.HasPrefix(entry, "PLUGIN_PRIVATE_REGISTRY_PASSWORD=") || strings.HasPrefix(entry, "PLUGIN_BUILD_COMMAND_OPTIONS=") {
				t.Fatal("secret plugin setting inherited by child")
			}
		}
	}
	if !strings.Contains(strings.Join(sam.Environment, "\n"), "CUSTOM_BUILD_ENV=keep-me") {
		t.Fatal("unrelated environment lost")
	}
	requireCleanTemp(t, tmp)
}

func TestExecForwardsSAMSyntaxWithoutParsingOrRewritingConfig(t *testing.T) {
	for _, extension := range []string{".toml", ".yaml", ".yml"} {
		t.Run(extension, func(t *testing.T) {
			dir, tmp := helperCommands(t)
			project := t.TempDir()
			config := fixture(t, filepath.Join(project, "samconfig"+extension), "# retain comments and ordering\nversion = 0.1\n")
			if extension != ".toml" {
				fixture(t, config, "# numeric YAML keys are SAM's concern\nversion: 0.1\n42: unchanged\n")
			}
			if err := os.Chmod(config, 0640); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(config)
			beforeInfo, _ := os.Stat(config)
			env := fixture(t, filepath.Join(project, "env.json"), "null")
			options := fmt.Sprintf("--config-file %s -bi -tfoo/img:1 --output -e-output -ef=%s -eTOKEN=first --container-env-var=Function1.TOKEN=second --save-params -- --not-an-option", config, env)
			if err := Exec(context.Background(), Args{TemplateFilePath: "template.yaml", BuildCommandOptions: options}); err != nil {
				t.Fatal(err)
			}
			sam := readInvocation(t, dir, "sam")
			want := []string{"build", "--template-file", "template.yaml", "--config-file", config, "-bi", "-tfoo/img:1", "--output", "-e-output", "-ef=" + env, "-eTOKEN=first", "--container-env-var=Function1.TOKEN=second", "--save-params", "--", "--not-an-option"}
			if !reflect.DeepEqual(sam.Args, want) {
				t.Fatalf("SAM syntax altered: %q", sam.Args)
			}
			after, _ := os.ReadFile(config)
			afterInfo, _ := os.Stat(config)
			if string(before) != string(after) || !os.SameFile(beforeInfo, afterInfo) || afterInfo.Mode().Perm() != 0640 {
				t.Fatal("bridge modified config content, inode, or mode")
			}
			requireCleanTemp(t, tmp)
		})
	}
}

func TestNativeSaveBeforeFailedBuildIsNotUndone(t *testing.T) {
	_, tmp := helperCommands(t)
	config := fixture(t, filepath.Join(t.TempDir(), "samconfig.toml"), "# original\nversion=0.1\n")
	os.Chmod(config, 0644)
	t.Setenv("SAM_PLUGIN_TEST_NATIVE_SAVE", config)
	t.Setenv("SAM_PLUGIN_TEST_EXIT_SAM", "1")
	err := Exec(context.Background(), Args{TemplateFilePath: "template.yaml", BuildCommandOptions: "--config-file " + config + " --save-params -e TOKEN=dummy"})
	if err == nil {
		t.Fatal("expected native build failure")
	}
	contents, _ := os.ReadFile(config)
	info, _ := os.Stat(config)
	if string(contents) != "# original\nversion=0.1\n# native-save-before-build\n" || info.Mode().Perm() != 0644 {
		t.Fatal("native save was changed or rolled back")
	}
	requireCleanTemp(t, tmp)
}

func TestExecHandlesFailureAndCancellationWithoutCredentialFiles(t *testing.T) {
	for _, scenario := range []string{"login-error", "build-error", "cancel-login", "cancel-build"} {
		t.Run(scenario, func(t *testing.T) {
			dir, tmp := helperCommands(t)
			command := "sam"
			switch scenario {
			case "login-error":
				t.Setenv("SAM_PLUGIN_TEST_EXIT_DOCKER", "1")
				t.Setenv("SAM_PLUGIN_TEST_OUTPUT_DOCKER", "rejected dummy-password")
			case "build-error":
				t.Setenv("SAM_PLUGIN_TEST_EXIT_SAM", "1")
			case "cancel-login":
				command = "docker"
				t.Setenv("SAM_PLUGIN_TEST_SLEEP", command)
			case "cancel-build":
				t.Setenv("SAM_PLUGIN_TEST_SLEEP", command)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if strings.HasPrefix(scenario, "cancel-") {
				go func() {
					for {
						if _, err := os.Stat(filepath.Join(dir, command+".json")); err == nil {
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(5 * time.Millisecond):
						}
					}
				}()
			}
			err := Exec(ctx, Args{PrivateRegistryURL: "example.invalid", PrivateRegistryUsername: "user", PrivateRegistryPassword: "dummy-password", TemplateFilePath: "template.yaml", BuildCommandOptions: "-e TOKEN=dummy-build-token"})
			if err == nil || strings.Contains(err.Error(), "dummy-password") || strings.Contains(err.Error(), "dummy-build-token") {
				t.Fatalf("expected sanitized failure, got %v", err)
			}
			if scenario == "login-error" || scenario == "cancel-login" {
				if _, err := os.Stat(filepath.Join(dir, "sam.json")); !os.IsNotExist(err) {
					t.Fatal("SAM started after failed login")
				}
			}
			requireCleanTemp(t, tmp)
		})
	}
}

func TestDedicatedContainerEnvironmentFile(t *testing.T) {
	dir, tmp := helperCommands(t)
	file := fixture(t, filepath.Join(t.TempDir(), "env.json"), `{"Parameters":{"TOKEN":"dummy"}}`)
	if err := Exec(context.Background(), Args{TemplateFilePath: "template.yml", ContainerEnvVarFile: file}); err != nil {
		t.Fatal(err)
	}
	sam := readInvocation(t, dir, "sam")
	want := []string{"build", "--template-file", "template.yml", "--container-env-var-file", file}
	if !reflect.DeepEqual(sam.Args, want) {
		t.Fatalf("wrong file setting: %q", sam.Args)
	}
	requireCleanTemp(t, tmp)
}

func TestInvalidOptionsAndMissingCredentials(t *testing.T) {
	dir, tmp := helperCommands(t)
	for _, args := range []Args{{BuildCommandOptions: `-e TOKEN="dummy`}, {PrivateRegistryURL: "example.invalid"}} {
		err := Exec(context.Background(), args)
		if err == nil || strings.Contains(err.Error(), "dummy") {
			t.Fatalf("expected sanitized validation failure: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sam.json")); !os.IsNotExist(err) {
		t.Fatal("SAM started with invalid plugin inputs")
	}
	requireCleanTemp(t, tmp)
}
