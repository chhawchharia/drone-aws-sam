// Copyright 2026 Harness. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// This is the console entry point installed by aws-sam-cli in the plugin image.
// Set Python's in-memory argv, not the operating system's process arguments.
// SAM and Click retain ownership of option parsing, config loading/saving and
// environment-variable precedence. No credential-bearing temporary file is made.
const samEntrypoint = `import json, os, sys
sys.argv = ["sam"] + json.load(sys.stdin)
sys.stdin = open(os.devnull)
from samcli.cli.main import cli
cli()
`

func samCommand(ctx context.Context, arguments []string) (*exec.Cmd, error) {
	payload, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("could not encode SAM build options")
	}
	cmd := exec.CommandContext(ctx, "python3", "-c", samEntrypoint)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = childEnvironment()
	return cmd, nil
}

func childEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if key != "PLUGIN_PRIVATE_REGISTRY_PASSWORD" && key != "PLUGIN_BUILD_COMMAND_OPTIONS" {
			result = append(result, entry)
		}
	}
	return result
}
