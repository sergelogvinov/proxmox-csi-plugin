/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Proxmox PV Migrate utility
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	cobra "github.com/spf13/cobra"

	clilog "github.com/leahcimic/proxmox-csi-plugin/pkg/log"
)

var (
	command = "pvecsictl"
	version = "v0.0.0"
	commit  = "none"

	cloudconfig string
	kubeconfig  string

	flagLogLevel = "log-level"

	flagProxmoxConfig = "config"
	flagKubeConfig    = "kubeconfig"

	logger *log.Entry
)

func main() {
	if exitCode := run(); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := log.New()
	l.SetOutput(os.Stdout)
	l.SetLevel(log.InfoLevel)

	logger = l.WithContext(ctx)

	cmd := cobra.Command{
		Use:     command,
		Version: fmt.Sprintf("%s (commit: %s)", version, commit),
		Short:   "A command-line utility to manipulate PersistentVolume/PersistentVolumeClaim on Proxmox VE",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			loglvl, _ := f.GetString(flagLogLevel) //nolint: errcheck

			clilog.Configure(logger, loglvl)

			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().String(flagLogLevel, clilog.LevelInfo,
		fmt.Sprintf("log level, must be one of: %s", strings.Join(clilog.Levels, ", ")))

	cmd.PersistentFlags().StringVar(&cloudconfig, flagProxmoxConfig, "", "proxmox cluster config file")
	cmd.PersistentFlags().StringVar(&kubeconfig, flagKubeConfig, "", "kubernetes config file")

	cmd.AddCommand(buildMigrateCmd())
	cmd.AddCommand(buildRenameCmd())
	cmd.AddCommand(buildSwapCmd())

	err := cmd.ExecuteContext(ctx)
	if err != nil {
		errorString := err.Error()
		if strings.Contains(errorString, "arg(s)") || strings.Contains(errorString, "flag") || strings.Contains(errorString, "command") {
			fmt.Fprintf(os.Stderr, "Error: %s\n\n", errorString)
			fmt.Fprintln(os.Stderr, cmd.UsageString())
		} else {
			logger.Errorf("Error: %s\n", errorString)
		}

		return 1
	}

	return 0
}
