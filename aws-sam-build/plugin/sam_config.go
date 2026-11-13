// Copyright 2026 Harness. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type buildConfig struct {
	environmentFile string
	extension       string
	sanitized       []byte
	saveParams      bool
	path            string
	environment     string
	document        map[string]interface{}
	globalInline    interface{}
	hadGlobalInline bool
	buildFile       interface{}
	hadBuildFile    bool
}

// SAM resolves samconfig beside the original template, then overlays the selected
// environment's build parameters on its global parameters. Inline CLI variables
// override config's entire container_env_var option. If we remove those CLI
// variables, remove the now-shadowed config option from a private config copy as
// well; otherwise SAM would unexpectedly apply it again.
func loadBuildConfig(options []string, includeEnvironmentFile bool) (buildConfig, error) {
	result := buildConfig{}
	template := lastOptionValue(options, "--template-file", "--template", "-t")
	configFile := lastOptionValue(options, "--config-file")
	environment := lastOptionValue(options, "--config-env")
	if environment == "" {
		environment = "default"
	}
	result.environment = environment
	dir := filepath.Dir(template)
	result.path = filepath.Join(dir, "samconfig.toml")
	result.extension = ".toml"
	result.document = map[string]interface{}{"version": 0.1}
	if configFile == "" {
		for _, extension := range []string{".toml", ".yaml", ".yml"} {
			path := filepath.Join(dir, "samconfig"+extension)
			if _, err := os.Stat(path); err == nil {
				configFile = "samconfig" + extension
				break
			}
		}
		if configFile == "" {
			return result, nil
		}
	}
	path := configFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	result.path = path
	contents, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) && configFile == "samconfig.toml" {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("could not read SAM configuration for container environment conversion")
	}
	result.extension = filepath.Ext(path)
	var document map[string]interface{}
	switch result.extension {
	case ".toml":
		err = toml.Unmarshal(contents, &document)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(contents, &document)
	default:
		return result, fmt.Errorf("unsupported SAM configuration format")
	}
	if err != nil {
		return result, fmt.Errorf("could not parse SAM configuration for container environment conversion")
	}
	result.document = document
	selected, _ := document[environment].(map[string]interface{})
	var shadowedInline bool
	for _, section := range []string{"global", "build"} {
		command, _ := selected[section].(map[string]interface{})
		parameters, _ := command["parameters"].(map[string]interface{})
		if section == "global" {
			result.globalInline, result.hadGlobalInline = parameters["container_env_var"]
		} else {
			result.buildFile, result.hadBuildFile = parameters["container_env_var_file"]
		}
		if value, ok := parameters["save_params"]; ok {
			// Click accepts native booleans and the usual boolean strings.
			text := strings.ToLower(fmt.Sprint(value))
			result.saveParams = text == "true" || text == "1" || text == "yes" || text == "y" || text == "on" || text == "t"
		}
		if value, ok := parameters["container_env_var_file"]; ok && includeEnvironmentFile {
			var valid bool
			result.environmentFile, valid = value.(string)
			if !valid {
				return result, fmt.Errorf("SAM container_env_var_file must be a path string")
			}
		}
		if _, ok := parameters["container_env_var"]; ok {
			shadowedInline = true
			delete(parameters, "container_env_var")
		}
	}
	if !shadowedInline {
		return result, nil
	}
	result.sanitized, err = marshalSAMConfig(document, result.extension)
	if err != nil {
		return result, fmt.Errorf("could not prepare temporary SAM configuration")
	}
	return result, nil
}

func marshalSAMConfig(document map[string]interface{}, extension string) ([]byte, error) {
	if extension == ".toml" {
		return toml.Marshal(document)
	}
	return yaml.Marshal(document)
}

func lastOptionValue(options []string, names ...string) string {
	var value string
	for i := 0; i < len(options); i++ {
		if options[i] == "--" {
			break
		}
		option := options[i]
		matched := false
		for _, name := range names {
			if options[i] == name && i+1 < len(options) {
				i++
				value = options[i]
				matched = true
				break
			}
			if strings.HasPrefix(options[i], name+"=") {
				value = strings.TrimPrefix(options[i], name+"=")
				matched = true
				break
			}
			if len(name) == 2 && strings.HasPrefix(name, "-") && strings.HasPrefix(options[i], name) {
				value = strings.TrimPrefix(options[i], name)
				matched = true
				break
			}
		}
		if !matched && isSAMValueOption(option) && i+1 < len(options) {
			i++
		}
	}
	return value
}
