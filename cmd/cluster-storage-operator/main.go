package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	k8sflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"

	"github.com/openshift/library-go/pkg/controller/controllercmd"

	"github.com/openshift/cluster-storage-operator/pkg/operator"
	"github.com/openshift/cluster-storage-operator/pkg/version"
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(k8sflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	logs.InitLogs()
	logs.AddFlags(pflag.CommandLine)
	defer logs.FlushLogs()

	command := NewOperatorCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func NewOperatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-storage-operator",
		Short: "OpenShift Cluster Storage Operator",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	ctrlCmd := controllercmd.NewControllerCommandConfig(
		"cluster-storage-operator",
		version.Get(),
		operator.RunOperator,
	).NewCommand()
	ctrlCmd.Use = "start"
	ctrlCmd.Short = "Start the Cluster Storage Operator"

	cmd.AddCommand(ctrlCmd)

	return cmd
}
