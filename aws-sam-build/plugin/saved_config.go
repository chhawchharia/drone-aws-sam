// Copyright 2026 Harness. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// SAM can save build parameters on a successful build. Let it save against the
// private config copy, then restore the user's original inline option values and
// env-file selection before publishing that config at its original location.
// This preserves --save-params without retaining an expired temporary file path.
func (config buildConfig) persistSavedParameters(temporaryConfig, temporaryEnv string, inline []string, file string, explicitFile bool) error {
	contents, err := ioutil.ReadFile(temporaryConfig)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read SAM's saved build parameters")
	}
	var document map[string]interface{}
	if config.extension == ".toml" {
		err = toml.Unmarshal(contents, &document)
	} else {
		err = yaml.Unmarshal(contents, &document)
	}
	if err != nil {
		return fmt.Errorf("could not parse SAM's saved build parameters")
	}
	selected, _ := document[config.environment].(map[string]interface{})
	build, _ := selected["build"].(map[string]interface{})
	parameters, _ := build["parameters"].(map[string]interface{})
	if parameters["container_env_var_file"] != temporaryEnv {
		// A failed/cancelled command did not save its generated env-file option.
		return nil
	}
	parameters["container_env_var"] = inline
	if explicitFile {
		parameters["container_env_var_file"] = file
	} else if config.hadBuildFile {
		parameters["container_env_var_file"] = config.buildFile
	} else {
		delete(parameters, "container_env_var_file")
	}
	if config.hadGlobalInline {
		global, _ := selected["global"].(map[string]interface{})
		globalParameters, _ := global["parameters"].(map[string]interface{})
		if globalParameters == nil {
			globalParameters = make(map[string]interface{})
			if global == nil {
				global = make(map[string]interface{})
				selected["global"] = global
			}
			global["parameters"] = globalParameters
		}
		globalParameters["container_env_var"] = config.globalInline
	}
	encoded, err := marshalSAMConfig(document, config.extension)
	if err != nil {
		return fmt.Errorf("could not encode SAM's saved build parameters")
	}
	path := config.path
	if _, err := os.Lstat(path); err == nil {
		// Match SAM's behavior for an existing symlink to a config file.
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("could not resolve SAM's saved configuration path")
		}
	}
	output, err := ioutil.TempFile(filepath.Dir(path), ".samconfig-credentials-")
	if err != nil {
		return fmt.Errorf("could not create SAM's saved configuration file")
	}
	defer os.Remove(output.Name())
	_, writeErr := output.Write(encoded)
	closeErr := output.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("could not write SAM's saved configuration file")
	}
	if err := os.Rename(output.Name(), path); err != nil {
		return fmt.Errorf("could not replace SAM's saved configuration file")
	}
	return nil
}
