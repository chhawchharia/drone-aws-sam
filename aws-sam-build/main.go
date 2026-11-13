// Copyright 2020 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/harness-community/aws-sam-build/plugin"

	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
)

func main() {
	logrus.SetFormatter(new(formatter))
	if err := run(); err != nil {
		logrus.Fatalln(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var args plugin.Args
	if err := envconfig.Process("", &args); err != nil {
		return err
	}
	// Drop consumed secret settings from future environment lookups and children.
	// Unsetenv cannot erase Linux's initial /proc/<pid>/environ memory region.
	_ = os.Unsetenv("PLUGIN_PRIVATE_REGISTRY_PASSWORD")
	_ = os.Unsetenv("PLUGIN_BUILD_COMMAND_OPTIONS")

	switch args.Level {
	case "debug":
		logrus.SetFormatter(textFormatter)
		logrus.SetLevel(logrus.DebugLevel)
	case "trace":
		logrus.SetFormatter(textFormatter)
		logrus.SetLevel(logrus.TraceLevel)
	}

	return plugin.Exec(ctx, args)
}

// default formatter that writes logs without including timestamp
// or level information.
type formatter struct{}

func (*formatter) Format(entry *logrus.Entry) ([]byte, error) {
	return []byte(entry.Message), nil
}

// text formatter that writes logs with level information
var textFormatter = &logrus.TextFormatter{
	DisableTimestamp: true,
}
