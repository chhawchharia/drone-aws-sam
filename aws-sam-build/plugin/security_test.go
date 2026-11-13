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
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type invocation struct {
	Args          []string
	Stdin         string
	Environment   []string
	EnvPath       string
	EnvJSON       map[string]map[string]interface{}
	FileMode      os.FileMode
	DirectoryMode os.FileMode
	ConfigPath    string
	Config        string
}

// Invoke the actual Exec entrypoint with child executables that capture their
// real argv/stdin and read the env file while it exists. No registry, Docker
// daemon, or customer credentials are used by these tests.
func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if os.Getenv("SAM_PLUGIN_TEST_HELPER") != "1" || (name != "docker" && name != "sam") {
		os.Exit(m.Run())
	}
	input, _ := io.ReadAll(os.Stdin)
	record := invocation{Args: os.Args[1:], Stdin: string(input), Environment: os.Environ()}
	if name == "sam" {
		record.EnvPath = lastOptionValue(record.Args, "--container-env-var-file", "-ef")
		if record.EnvPath != "" {
			contents, err := os.ReadFile(record.EnvPath)
			if err != nil {
				os.Exit(20)
			}
			if json.Unmarshal(contents, &record.EnvJSON) != nil {
				os.Exit(21)
			}
			info, _ := os.Stat(record.EnvPath)
			record.FileMode = info.Mode().Perm()
			info, _ = os.Stat(filepath.Dir(record.EnvPath))
			record.DirectoryMode = info.Mode().Perm()
		}
		record.ConfigPath = lastOptionValue(record.Args, "--config-file")
		if record.ConfigPath != "" {
			contents, _ := os.ReadFile(record.ConfigPath)
			record.Config = string(contents)
			var document map[string]interface{}
			if filepath.Ext(record.ConfigPath) == ".toml" {
				_ = toml.Unmarshal(contents, &document)
			} else {
				_ = yaml.Unmarshal(contents, &document)
			}
			environment := lastOptionValue(record.Args, "--config-env")
			if environment == "" {
				environment = "default"
			}
			selected, _ := document[environment].(map[string]interface{})
			build, _ := selected["build"].(map[string]interface{})
			parameters, _ := build["parameters"].(map[string]interface{})
			global, _ := selected["global"].(map[string]interface{})
			globalParameters, _ := global["parameters"].(map[string]interface{})
			save := hasSAMFlag(record.Args, "--save-params") || parameters["save_params"] == true || globalParameters["save_params"] == true
			if save && os.Getenv("SAM_PLUGIN_TEST_EXIT_SAM") == "" && os.Getenv("SAM_PLUGIN_TEST_SLEEP") != "sam" {
				if selected == nil {
					selected = make(map[string]interface{})
					document[environment] = selected
				}
				if build == nil {
					build = make(map[string]interface{})
					selected["build"] = build
				}
				if parameters == nil {
					parameters = make(map[string]interface{})
					build["parameters"] = parameters
				}
				// Model SAM's saver writing its CLI env-file parameter plus
				// another build parameter to the selected config environment.
				parameters["container_env_var_file"] = record.EnvPath
				parameters["cached"] = true
				updated, _ := marshalSAMConfig(document, filepath.Ext(record.ConfigPath))
				if os.WriteFile(record.ConfigPath, updated, 0600) != nil {
					os.Exit(23)
				}
			}
		}
	}
	encoded, _ := json.Marshal(record)
	if os.WriteFile(filepath.Join(os.Getenv("SAM_PLUGIN_TEST_CAPTURE"), name+".json"), encoded, 0600) != nil {
		os.Exit(22)
	}
	if os.Getenv("SAM_PLUGIN_TEST_SLEEP") == name {
		time.Sleep(time.Minute)
	}
	fmt.Print(os.Getenv("SAM_PLUGIN_TEST_OUTPUT_" + strings.ToUpper(name)))
	code, _ := strconv.Atoi(os.Getenv("SAM_PLUGIN_TEST_EXIT_" + strings.ToUpper(name)))
	os.Exit(code)
}

func helperCommands(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	tmp := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker", "sam"} {
		if err := os.Symlink(executable, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", tmp)
	t.Setenv("SAM_PLUGIN_TEST_HELPER", "1")
	t.Setenv("SAM_PLUGIN_TEST_CAPTURE", dir)
	// Race-instrumented child helpers do not need the runtime's default
	// one-second exit delay before the next command can start.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	return dir, tmp
}

func readInvocation(t *testing.T, dir, command string) invocation {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(dir, command+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record invocation
	if err := json.Unmarshal(contents, &record); err != nil {
		t.Fatal(err)
	}
	return record
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

func requireCleanTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary credential files remain: %v", entries)
	}
}

func TestExecKeepsCredentialsOutOfArguments(t *testing.T) {
	dir, tmp := helperCommands(t)
	password := "dummy-registry-token-'$()"
	options := `-e JFROG_PASSWORD=dummy-build-token -e GRADLE_OPTS="-Dgradle.wrapperUser=gitlab -Dgradle.wrapperPassword=dummy-gradle-token" --parallel`
	t.Setenv("PLUGIN_PRIVATE_REGISTRY_PASSWORD", password)
	t.Setenv("PLUGIN_BUILD_COMMAND_OPTIONS", options)
	t.Setenv("CUSTOM_BUILD_ENV", "keep-me")
	err := Exec(context.Background(), Args{
		PrivateRegistryURL: "example.invalid", PrivateRegistryUsername: "gitlab", PrivateRegistryPassword: password,
		TemplateFilePath: "template.yaml", BuildImage: "example.invalid/build:dummy", BuildCommandOptions: options,
	})
	if err != nil {
		t.Fatal(err)
	}
	docker := readInvocation(t, dir, "docker")
	wantDocker := []string{"login", "example.invalid", "--username", "gitlab", "--password-stdin"}
	if !reflect.DeepEqual(docker.Args, wantDocker) || docker.Stdin != password {
		t.Fatalf("unexpected login transport: %#v", docker)
	}
	sam := readInvocation(t, dir, "sam")
	for _, command := range []invocation{docker, sam} {
		for _, secret := range []string{password, "dummy-build-token", "dummy-gradle-token"} {
			if strings.Contains(strings.Join(command.Args, " "), secret) {
				t.Fatalf("secret in child arguments: %q", command.Args)
			}
		}
		for _, entry := range command.Environment {
			if strings.HasPrefix(entry, "PLUGIN_PRIVATE_REGISTRY_PASSWORD=") || strings.HasPrefix(entry, "PLUGIN_BUILD_COMMAND_OPTIONS=") {
				t.Fatalf("plugin credential setting inherited by child: %s", entry)
			}
		}
	}
	if sam.EnvJSON["Parameters"]["JFROG_PASSWORD"] != "dummy-build-token" || sam.EnvJSON["Parameters"]["GRADLE_OPTS"] != "-Dgradle.wrapperUser=gitlab -Dgradle.wrapperPassword=dummy-gradle-token" {
		t.Fatalf("build values changed: %#v", sam.EnvJSON)
	}
	if sam.FileMode != 0600 || sam.DirectoryMode != 0700 {
		t.Fatalf("unsafe temporary permissions: %o / %o", sam.FileMode, sam.DirectoryMode)
	}
	if !strings.Contains(strings.Join(sam.Environment, "\n"), "CUSTOM_BUILD_ENV=keep-me") {
		t.Fatal("unrelated environment was not preserved")
	}
	if _, err := os.Stat(sam.EnvPath); !os.IsNotExist(err) {
		t.Fatalf("credential file was not removed: %v", err)
	}
	requireCleanTemp(t, tmp)
}

func TestExecCleansUpOnFailureAndCancellation(t *testing.T) {
	for _, scenario := range []string{"login-error", "build-error", "cancel-login", "cancel-build"} {
		t.Run(scenario, func(t *testing.T) {
			dir, tmp := helperCommands(t)
			switch scenario {
			case "login-error":
				t.Setenv("SAM_PLUGIN_TEST_EXIT_DOCKER", "1")
				t.Setenv("SAM_PLUGIN_TEST_OUTPUT_DOCKER", "rejected dummy-password")
			case "build-error":
				t.Setenv("SAM_PLUGIN_TEST_EXIT_SAM", "1")
			case "cancel-login":
				t.Setenv("SAM_PLUGIN_TEST_SLEEP", "docker")
			case "cancel-build":
				t.Setenv("SAM_PLUGIN_TEST_SLEEP", "sam")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if scenario == "cancel-login" || scenario == "cancel-build" {
				command := "docker"
				if scenario == "cancel-build" {
					command = "sam"
				}
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
			err := Exec(ctx, Args{PrivateRegistryURL: "example.invalid", PrivateRegistryUsername: "user", PrivateRegistryPassword: "dummy-password", TemplateFilePath: "template.yaml", BuildCommandOptions: "-e PASSWORD=dummy-build-password"})
			if err == nil || strings.Contains(err.Error(), "dummy-password") || strings.Contains(err.Error(), "dummy-build-password") {
				t.Fatalf("expected sanitized failure, got %v", err)
			}
			requireCleanTemp(t, tmp)
			if scenario == "build-error" || scenario == "cancel-build" {
				readInvocation(t, dir, "sam")
			}
		})
	}
}

func TestContainerEnvOptionsAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := fixture(t, filepath.Join(dir, "existing.json"), `{"Parameters":{"TOKEN":"file-global","KEEP":"keep"},"Function1":{"TOKEN":"file-resource"}}`)
	original, _ := os.ReadFile(file)
	options := []string{"sam", "build", "--container-env-var-file", file, "-e", "TOKEN=inline-global", "--container-env-var=Function1.EXTRA=inline-resource", "-eTOKEN=last-global", "--container-env-var", "VALUE=a=b=c", "-e", "EMPTY=", "-e", "INVALID", "--cached", "--", "Function1"}
	result, cleanup, err := prepareContainerEnvFile(options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path := lastOptionValue(result, "--container-env-var-file")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var environment map[string]map[string]interface{}
	if err := json.Unmarshal(contents, &environment); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]interface{}{"Parameters": {"TOKEN": "last-global", "KEEP": "keep", "VALUE": "a=b=c"}, "Function1": {"TOKEN": "file-resource", "EXTRA": "inline-resource"}}
	if !reflect.DeepEqual(environment, want) {
		t.Fatalf("precedence changed: %#v", environment)
	}
	if strings.Contains(strings.Join(result, " "), "last-global") || !reflect.DeepEqual(result[len(result)-2:], []string{"--", "Function1"}) {
		t.Fatalf("unexpected arguments: %q", result)
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(original) {
		t.Fatal("original environment file changed")
	}
}

func TestContainerEnvFileAliasesAndLastFileWins(t *testing.T) {
	dir := t.TempDir()
	first := fixture(t, filepath.Join(dir, "first.json"), `{"Parameters":{"FIRST":"ignore"}}`)
	last := fixture(t, filepath.Join(dir, "last.json"), `{"Parameters":{"LAST":"keep"}}`)
	for _, flag := range [][]string{{"-ef", last}, {"-ef=" + last}, {"--container-env-var-file=" + last}} {
		t.Run(strings.Join(flag, " "), func(t *testing.T) {
			options := append([]string{"sam", "build", "--container-env-var-file", first}, flag...)
			options = append(options, "-e", "PASSWORD=dummy")
			result, cleanup, err := prepareContainerEnvFile(options)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			contents, _ := os.ReadFile(lastOptionValue(result, "--container-env-var-file"))
			if strings.Contains(string(contents), "FIRST") || !strings.Contains(string(contents), "LAST") {
				t.Fatalf("wrong file merged: %s", contents)
			}
		})
	}
}

func TestNoInlineVariablesPreserveArguments(t *testing.T) {
	for _, options := range [][]string{{"sam", "build", "--cached"}, {"sam", "build", "-ef", "not-readable-here.json"}, {"sam", "build", "--", "-e"}} {
		result, cleanup, err := prepareContainerEnvFile(options)
		defer cleanup()
		if err != nil || !reflect.DeepEqual(options, result) {
			t.Fatalf("unrelated options changed: %q -> %q: %v", options, result, err)
		}
	}
}

func TestInvalidEnvironmentFilesDoNotEchoSecrets(t *testing.T) {
	for _, contents := range []string{`{"Parameters":"dummy-secret"}`, `{"Parameters":null}`, `null`, `["dummy-secret"]`, `{"Parameters":{"KEY":"dummy-secret"`} {
		t.Run(contents, func(t *testing.T) {
			file := fixture(t, filepath.Join(t.TempDir(), "invalid.json"), contents)
			_, cleanup, err := prepareContainerEnvFile([]string{"sam", "build", "-ef", file, "-e", "KEY=dummy-other-secret"})
			defer cleanup()
			if err == nil || strings.Contains(err.Error(), "dummy-") {
				t.Fatalf("expected sanitized validation error, got %v", err)
			}
		})
	}
}

func TestSAMConfigEnvironmentFileAndInlineFallback(t *testing.T) {
	for _, format := range []string{".toml", ".yaml", ".yml"} {
		t.Run(format, func(t *testing.T) {
			dir, tmp := helperCommands(t)
			project := t.TempDir()
			template := fixture(t, filepath.Join(project, "template.yaml"), "Resources: {}")
			envFile := fixture(t, filepath.Join(project, "env.json"), `{"Parameters":{"TOKEN":"file-global","KEEP":"keep"},"Function1":{"TOKEN":"file-resource"}}`)
			config := fmt.Sprintf("version = 0.1\n[qa.global.parameters]\nregion = 'us-east-1'\ncontainer_env_var = ['TOKEN=ignored-global']\n[qa.build.parameters]\nparallel = true\ncontainer_env_var_file = '%s'\ncontainer_env_var = ['TOKEN=ignored-build']\n", envFile)
			if format != ".toml" {
				config = fmt.Sprintf("version: 0.1\nqa:\n  global:\n    parameters:\n      region: us-east-1\n      container_env_var: ['TOKEN=ignored-global']\n  build:\n    parameters:\n      parallel: true\n      container_env_var_file: '%s'\n      container_env_var: ['TOKEN=ignored-build']\n", envFile)
			}
			configPath := fixture(t, filepath.Join(project, "samconfig"+format), config)
			options := "--config-env qa -e TOKEN=inline-global -e Function1.EXTRA=inline-resource"
			if err := Exec(context.Background(), Args{TemplateFilePath: template, BuildCommandOptions: options}); err != nil {
				t.Fatal(err)
			}
			sam := readInvocation(t, dir, "sam")
			if sam.EnvJSON["Parameters"]["TOKEN"] != "inline-global" || sam.EnvJSON["Parameters"]["KEEP"] != "keep" || sam.EnvJSON["Function1"]["TOKEN"] != "file-resource" {
				t.Fatalf("SAM config precedence changed: %#v", sam.EnvJSON)
			}
			if strings.Contains(sam.Config, "container_env_var =") || strings.Contains(sam.Config, "container_env_var:") || !strings.Contains(sam.Config, "parallel") || !strings.Contains(sam.Config, "us-east-1") {
				t.Fatalf("unexpected sanitized SAM config: %s", sam.Config)
			}
			after, _ := os.ReadFile(configPath)
			if string(after) != config {
				t.Fatal("original SAM config changed")
			}
			requireCleanTemp(t, tmp)
		})
	}
}

func TestSaveParamsPreservesOptionsWithoutTemporaryReferences(t *testing.T) {
	for _, extension := range []string{".toml", ".yaml", ".yml"} {
		for _, scenario := range []string{"new", "inherited", "build-file", "explicit-file"} {
			t.Run(extension+"/"+scenario, func(t *testing.T) {
				dir, tmp := helperCommands(t)
				project := t.TempDir()
				template := fixture(t, filepath.Join(project, "template.yaml"), "Resources: {}")
				file := fixture(t, filepath.Join(project, "original-env.json"), `{"Parameters":{"KEEP":"original"}}`)
				configPath := filepath.Join(project, "samconfig"+extension)
				config := map[string]interface{}{"version": 0.1}
				if scenario != "new" {
					build := map[string]interface{}{"save_params": true, "container_env_var": []string{"TOKEN=ignored-build"}}
					if scenario == "build-file" {
						build["container_env_var_file"] = file
					}
					config["qa"] = map[string]interface{}{
						"global": map[string]interface{}{"parameters": map[string]interface{}{"container_env_var": []string{"TOKEN=ignored-global"}, "container_env_var_file": file, "region": "us-east-1"}},
						"build":  map[string]interface{}{"parameters": build},
					}
					encoded, _ := marshalSAMConfig(config, extension)
					fixture(t, configPath, string(encoded))
				}
				options := "--config-env qa -e TOKEN=dummy-secret"
				if scenario == "new" {
					options += " --save-params"
					// SAM requires custom config filenames to exist.
					if extension != ".toml" {
						options += " --config-file " + configPath
						encoded, _ := marshalSAMConfig(config, extension)
						fixture(t, configPath, string(encoded))
					}
				}
				if scenario == "explicit-file" {
					options += " --container-env-var-file " + file
				}
				if err := Exec(context.Background(), Args{TemplateFilePath: template, BuildCommandOptions: options}); err != nil {
					t.Fatal(err)
				}
				sam := readInvocation(t, dir, "sam")
				contents, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(contents), sam.EnvPath) || strings.Contains(string(contents), "aws-sam-build-env-") {
					t.Fatalf("saved config references temporary credentials: %s", contents)
				}
				var saved map[string]interface{}
				if extension == ".toml" {
					err = toml.Unmarshal(contents, &saved)
				} else {
					err = yaml.Unmarshal(contents, &saved)
				}
				if err != nil {
					t.Fatal(err)
				}
				selected := saved["qa"].(map[string]interface{})
				parameters := selected["build"].(map[string]interface{})["parameters"].(map[string]interface{})
				if fmt.Sprint(parameters["container_env_var"]) != "[TOKEN=dummy-secret]" || parameters["cached"] != true {
					t.Fatalf("saved parameters changed: %#v", parameters)
				}
				if scenario == "build-file" || scenario == "explicit-file" {
					if parameters["container_env_var_file"] != file {
						t.Fatalf("original file selection not restored: %#v", parameters)
					}
				} else if _, ok := parameters["container_env_var_file"]; ok {
					t.Fatal("saved build gained an environment-file override")
				}
				if scenario != "new" {
					global := selected["global"].(map[string]interface{})["parameters"].(map[string]interface{})
					if fmt.Sprint(global["container_env_var"]) != "[TOKEN=ignored-global]" || global["region"] != "us-east-1" {
						t.Fatalf("global settings changed: %#v", global)
					}
				}
				info, _ := os.Stat(configPath)
				if info.Mode().Perm() != 0600 {
					t.Fatalf("saved credentials are not owner-only: %o", info.Mode().Perm())
				}
				requireCleanTemp(t, tmp)
			})
		}
	}
}

func TestOptionValuesStartingWithEnvFlagArePreserved(t *testing.T) {
	options := []string{"sam", "build", "--template-file", "-example.yaml", "--template", "-example-alias.yaml", "-cd", "-example-cache", "--build-image", "-example-image", "--config-env", "-example-env", "--cached"}
	result, cleanup, err := prepareContainerEnvFile(options)
	defer cleanup()
	if err != nil || !reflect.DeepEqual(result, options) {
		t.Fatalf("option values were interpreted as flags: %q: %v", result, err)
	}
	if lastOptionValue([]string{"--build-image", "--config-file", "--config-file", "real.toml"}, "--config-file") != "real.toml" {
		t.Fatal("SAM config option discovery consumed another option's value")
	}
}

func TestSavedParametersDoNotModifyOriginalOnBuildFailure(t *testing.T) {
	_, tmp := helperCommands(t)
	project := t.TempDir()
	template := fixture(t, filepath.Join(project, "template.yaml"), "Resources: {}")
	contents := "version=0.1\n[default.build.parameters]\nsave_params=true\ncontainer_env_var=['TOKEN=original']\n"
	config := fixture(t, filepath.Join(project, "samconfig.toml"), contents)
	t.Setenv("SAM_PLUGIN_TEST_EXIT_SAM", "1")
	if err := Exec(context.Background(), Args{TemplateFilePath: template, BuildCommandOptions: "-e TOKEN=dummy-new"}); err == nil {
		t.Fatal("expected build failure")
	}
	after, _ := os.ReadFile(config)
	if string(after) != contents {
		t.Fatal("failed build modified the original configuration")
	}
	requireCleanTemp(t, tmp)
}

func TestSavedParametersWriteFailureIsSanitized(t *testing.T) {
	temporary := fixture(t, filepath.Join(t.TempDir(), "samconfig.toml"), "version=0.1\n[default.build.parameters]\ncontainer_env_var_file='temporary.json'\n")
	config := buildConfig{environment: "default", extension: ".toml", path: filepath.Join(t.TempDir(), "missing", "samconfig.toml")}
	err := config.persistSavedParameters(temporary, "temporary.json", []string{"TOKEN=dummy-secret"}, "", false)
	if err == nil || strings.Contains(err.Error(), "dummy-secret") {
		t.Fatalf("expected sanitized save failure, got %v", err)
	}
}

func TestExplicitEnvironmentFileOverridesSAMConfig(t *testing.T) {
	project := t.TempDir()
	template := fixture(t, filepath.Join(project, "template.yaml"), "Resources: {}")
	fixture(t, filepath.Join(project, "settings.toml"), "version=0.1\n[default.build.parameters]\ncontainer_env_var_file=['invalid-but-overridden']\ncontainer_env_var=['TOKEN=ignored']\n")
	file := fixture(t, filepath.Join(project, "env.json"), `{"Parameters":{"KEEP":"cli-file"}}`)
	options := []string{"sam", "build", "-t" + template, "--config-file=settings.toml", "--container-env-var-file", file, "-e", "TOKEN=dummy"}
	result, cleanup, err := prepareContainerEnvFile(options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	contents, _ := os.ReadFile(lastOptionValue(result, "--container-env-var-file"))
	if !strings.Contains(string(contents), "cli-file") {
		t.Fatalf("explicit environment file was not used: %s", contents)
	}
}

func TestValidationAndTemporaryDirectoryFailure(t *testing.T) {
	for _, options := range [][]string{{"sam", "build", "-e"}, {"sam", "build", "-ef"}, {"sam", "build", "-ef", "does-not-exist.json", "-e", "TOKEN=dummy"}} {
		_, cleanup, err := prepareContainerEnvFile(options)
		cleanup()
		if err == nil || strings.Contains(err.Error(), "dummy") {
			t.Fatalf("expected sanitized validation error: %v", err)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	_, cleanup, err := prepareContainerEnvFile([]string{"sam", "build", "-e", "TOKEN=dummy"})
	cleanup()
	if err == nil || strings.Contains(err.Error(), "dummy") {
		t.Fatalf("expected sanitized temporary-directory error: %v", err)
	}
}

func TestExistingEnvironmentNumbersArePreservedExactly(t *testing.T) {
	file := fixture(t, filepath.Join(t.TempDir(), "env.json"), `{"Parameters":{"LARGE":9007199254740993}}`)
	result, cleanup, err := prepareContainerEnvFile([]string{"sam", "build", "-ef", file, "-e", "TOKEN=dummy"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	contents, _ := os.ReadFile(lastOptionValue(result, "--container-env-var-file"))
	if !strings.Contains(string(contents), "9007199254740993") {
		t.Fatalf("existing JSON number lost precision: %s", contents)
	}
}
