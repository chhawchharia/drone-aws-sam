// Copyright 2020 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/google/shlex"
)

// Args provides plugin execution arguments.
type Args struct {
	Pipeline

	// Level defines the plugin log level.
	Level string `envconfig:"PLUGIN_LOG_LEVEL"`

	BuildImage              string `envconfig:"PLUGIN_BUILD_IMAGE"`
	TemplateFilePath        string `envconfig:"PLUGIN_TEMPLATE_FILE_PATH"`
	BuildCommandOptions     string `envconfig:"PLUGIN_BUILD_COMMAND_OPTIONS"`
	ContainerEnvVarFile     string `envconfig:"PLUGIN_CONTAINER_ENV_VAR_FILE"`
	PrivateRegistryURL      string `envconfig:"PLUGIN_PRIVATE_REGISTRY_URL"`
	PrivateRegistryUsername string `envconfig:"PLUGIN_PRIVATE_REGISTRY_USERNAME"`
	PrivateRegistryPassword string `envconfig:"PLUGIN_PRIVATE_REGISTRY_PASSWORD"`
}

// Exec executes the plugin.
func Exec(ctx context.Context, args Args) error {
	if args.TemplateFilePath == "" {
		if _, err := os.Stat("./template.yaml"); err == nil {
			args.TemplateFilePath = "./template.yaml"
		} else if _, err := os.Stat("./template.yml"); err == nil {
			args.TemplateFilePath = "./template.yml"
		}
	}

	// Create the initial command slice
	commandArgs := []string{"sam", "build"}

	// Include build image option if provided
	if args.BuildImage != "" {
		commandArgs = append(commandArgs, "--use-container", "--build-image", args.BuildImage)
	}

	// Include the template file path
	commandArgs = append(commandArgs, "--template-file", args.TemplateFilePath)
	if args.ContainerEnvVarFile != "" {
		commandArgs = append(commandArgs, "--container-env-var-file", args.ContainerEnvVarFile)
	}

	// Add the build command options by parsing them correctly respecting quotes
	if args.BuildCommandOptions != "" {
		options, err := shlex.Split(args.BuildCommandOptions)
		if err != nil {
			return fmt.Errorf("error parsing build command options: %v", err)
		}
		commandArgs = append(commandArgs, options...)
	}

	if args.PrivateRegistryURL != "" {
		if err := loginToPrivateRegistry(ctx, args); err != nil {
			return err
		}
	}

	cmd, err := samCommand(ctx, commandArgs[1:])
	if err != nil {
		return err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error executing command: %v", err)
	}

	return nil
}

func loginToPrivateRegistry(ctx context.Context, args Args) error {
	if args.PrivateRegistryUsername == "" || args.PrivateRegistryPassword == "" {
		return fmt.Errorf("private registry credentials not provided")
	}
	cmd := exec.CommandContext(ctx, "docker", "login", args.PrivateRegistryURL, "--username", args.PrivateRegistryUsername, "--password-stdin")
	cmd.Stdin = strings.NewReader(args.PrivateRegistryPassword)
	cmd.Env = childEnvironment()
	output, err := cmd.CombinedOutput()
	// A registry error must not echo its credential into the plugin's logs.
	message := strings.ReplaceAll(string(output), args.PrivateRegistryPassword, "[REDACTED]")
	if err != nil {
		return fmt.Errorf("error executing registry login: %v\nOutput:\n%s", err, message)
	}

	fmt.Println(message)
	return nil

}
