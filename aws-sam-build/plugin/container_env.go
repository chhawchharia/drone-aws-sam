// Copyright 2026 Harness. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// prepareContainerEnvFile preserves the public BUILD_COMMAND_OPTIONS interface,
// but keeps inline container environment values out of SAM's process arguments.
// The JSON file uses SAM's Parameters and resource-specific scope format.
func prepareContainerEnvFile(options []string) ([]string, func() error, error) {
	cleanup := func() error { return nil }
	remaining := make([]string, 0, len(options))
	inline := make(map[string]map[string]interface{})
	var file string
	var fileFound bool
	var inlineFound bool
	var inlineArguments []string
	for i := 0; i < len(options); i++ {
		option := options[i]
		if isSAMValueOption(option) && option != "-e" && option != "--container-env-var" && option != "-ef" && option != "--container-env-var-file" && i+1 < len(options) {
			remaining = append(remaining, option, options[i+1])
			i++
			continue
		}
		var value string
		var environment, environmentFile bool
		switch {
		case option == "--":
			remaining = append(remaining, options[i:]...)
			i = len(options)
			continue
		case option == "--container-env-var-file" || option == "-ef":
			environmentFile = true
			if i+1 == len(options) {
				return nil, cleanup, fmt.Errorf("container environment file option requires a path")
			}
			i++
			value = options[i]
		case strings.HasPrefix(option, "--container-env-var-file="):
			environmentFile = true
			value = strings.TrimPrefix(option, "--container-env-var-file=")
		case strings.HasPrefix(option, "-ef="):
			environmentFile = true
			value = strings.TrimPrefix(option, "-ef=")
		case option == "--container-env-var" || option == "-e":
			environment = true
			if i+1 == len(options) {
				return nil, cleanup, fmt.Errorf("container environment option requires KEY=VALUE")
			}
			i++
			value = options[i]
		case strings.HasPrefix(option, "--container-env-var="):
			environment = true
			value = strings.TrimPrefix(option, "--container-env-var=")
		case strings.HasPrefix(option, "-e") && len(option) > 2:
			environment = true
			value = strings.TrimPrefix(option, "-e")
		}
		switch {
		case environmentFile:
			// Click uses the last value for this non-repeatable option.
			file = value
			fileFound = true
		case environment:
			inlineFound = true
			inlineArguments = append(inlineArguments, value)
			addContainerEnv(inline, value)
		default:
			remaining = append(remaining, option)
		}
	}
	if !inlineFound {
		// Leave existing env-file handling entirely to SAM when no conversion
		// is needed (including its normal path and validation behavior).
		return options, cleanup, nil
	}
	config, err := loadBuildConfig(options, !fileFound)
	if err != nil {
		return nil, cleanup, err
	}
	config.saveParams = config.saveParams || hasSAMFlag(options, "--save-params")
	if config.saveParams && config.sanitized == nil {
		config.sanitized, err = marshalSAMConfig(config.document, config.extension)
		if err != nil {
			return nil, cleanup, fmt.Errorf("could not prepare temporary SAM configuration")
		}
	}
	if !fileFound {
		file = config.environmentFile
	}

	environment := make(map[string]map[string]interface{})
	if file != "" {
		contents, err := ioutil.ReadFile(file)
		if err != nil {
			return nil, cleanup, fmt.Errorf("could not read container environment file")
		}
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.UseNumber()
		if err := decoder.Decode(&environment); err != nil || environment == nil {
			// JSON parser errors can contain the offending secret value.
			return nil, cleanup, fmt.Errorf("container environment file must contain a JSON object of environment objects")
		}
		var trailing interface{}
		if decoder.Decode(&trailing) != io.EOF {
			return nil, cleanup, fmt.Errorf("container environment file must contain a single JSON object")
		}
		for _, values := range environment {
			if values == nil {
				return nil, cleanup, fmt.Errorf("container environment file scopes must contain JSON objects")
			}
		}
	}
	for scope, values := range inline {
		if environment[scope] == nil {
			environment[scope] = make(map[string]interface{})
		}
		for name, value := range values {
			environment[scope][name] = value
		}
	}

	dir, err := ioutil.TempDir("", "aws-sam-build-env-")
	if err != nil {
		return nil, cleanup, fmt.Errorf("could not create temporary container environment directory")
	}
	path := filepath.Join(dir, "env.json")
	var configPath string
	cleanup = func() error {
		var saveErr error
		if config.saveParams {
			saveErr = config.persistSavedParameters(configPath, path, inlineArguments, file, fileFound)
		}
		var removeErr error
		if err := os.RemoveAll(dir); err != nil {
			removeErr = fmt.Errorf("could not remove temporary container credential files")
		}
		return errors.Join(saveErr, removeErr)
	}
	if config.sanitized != nil {
		configPath = filepath.Join(dir, "samconfig"+config.extension)
		if err := ioutil.WriteFile(configPath, config.sanitized, 0600); err != nil {
			cleanup()
			return nil, func() error { return nil }, fmt.Errorf("could not write temporary SAM configuration")
		}
		remaining = appendBeforeTerminator(remaining, "--config-file", configPath)
	}
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		cleanup()
		return nil, func() error { return nil }, fmt.Errorf("could not create temporary container environment file")
	}
	writeErr := json.NewEncoder(output).Encode(environment)
	closeErr := output.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		return nil, func() error { return nil }, fmt.Errorf("could not write temporary container environment file")
	}
	return appendBeforeTerminator(remaining, "--container-env-var-file", path), cleanup, nil
}

func hasSAMFlag(options []string, flag string) bool {
	for i := 0; i < len(options); i++ {
		if options[i] == "--" {
			break
		}
		if options[i] == flag {
			return true
		}
		if isSAMValueOption(options[i]) && i+1 < len(options) {
			i++
		}
	}
	return false
}

// Do not interpret an option's value as a second option. In particular, a
// legitimate template/config/image path can itself begin with '-e'.
func isSAMValueOption(option string) bool {
	switch option {
	case "--template-file", "--template", "-t", "--base-dir", "-s", "--build-dir", "-b",
		"--cache-dir", "-cd", "--manifest", "-m", "--docker-network", "--build-image",
		"--container-env-var", "-e", "--container-env-var-file", "-ef",
		"--config-file", "--config-env", "--parameter-overrides", "--exclude", "-x",
		"--mount-with", "-mw", "--hook-name", "--terraform-project-root-path", "--profile", "--region":
		return true
	}
	return false
}

func appendBeforeTerminator(options []string, extra ...string) []string {
	for i, option := range options {
		if option == "--" {
			result := append([]string{}, options[:i]...)
			result = append(result, extra...)
			return append(result, options[i:]...)
		}
	}
	return append(options, extra...)
}

func addContainerEnv(environment map[string]map[string]interface{}, input string) {
	pair := strings.SplitN(input, "=", 2)
	if len(pair) != 2 {
		return
	}
	name, value := strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1])
	if name == "" || value == "" {
		// Match SAM's process_env_var: empty or malformed entries are skipped.
		// Do not repeat the input in an error or log message.
		return
	}
	scope := "Parameters"
	if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
		scope, name = parts[0], parts[1]
		if strings.TrimSpace(scope) == "" || strings.TrimSpace(name) == "" {
			return
		}
	}
	if environment[scope] == nil {
		environment[scope] = make(map[string]interface{})
	}
	environment[scope][name] = value
}

func childEnvironment() []string {
	environment := os.Environ()
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key := strings.SplitN(entry, "=", 2)[0]
		if key != "PLUGIN_PRIVATE_REGISTRY_PASSWORD" && key != "PLUGIN_BUILD_COMMAND_OPTIONS" {
			result = append(result, entry)
		}
	}
	return result
}
