package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"k8s.io/component-base/cli"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/controllercmd"

	"github.com/openshift/cluster-storage-operator/pkg/operator"
	"github.com/openshift/cluster-storage-operator/pkg/version"
)

var (
	guestKubeConfig *string
)

func main() {
	command := NewOperatorCommand()
	code := cli.Run(command)
	os.Exit(code)
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
		runOperatorWithGuestKubeconfig,
		clock.RealClock{},
	).NewCommand()
	ctrlCmd.Use = "start"
	ctrlCmd.Short = "Start the Cluster Storage Operator"
	guestKubeConfig = ctrlCmd.Flags().String("guest-kubeconfig", "", "Path to guest kubeconfig file. This flag enables hypershift integration")

	cmd.AddCommand(ctrlCmd)

	return cmd
}

func runOperatorWithGuestKubeconfig(ctx context.Context, controllerConfig *controllercmd.ControllerContext) error {
	return operator.RunOperator(ctx, controllerConfig, guestKubeConfig)
}
