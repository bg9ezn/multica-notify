package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bg9ezn/multica-notify/internal/manifest"
)

func newInitPluginCmd() *cobra.Command {
	var (
		bridgeHost string
		out        string
		force      bool
	)
	cmd := &cobra.Command{
		Use:   "init-plugin --bridge-host <host>",
		Short: "Generate the Multica plugin manifest for this bridge",
		Long: "Writes multica.plugin.json with every BRIDGE_HOST_PLACEHOLDER filled\n" +
			"in - transport URLs, net: scope. Publish the generated file to Multica\n" +
			"afterwards (workspace Settings -> Plugins, or the packages API).",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := manifest.Write(out, bridgeHost, force); err != nil {
				return err
			}
			fmt.Printf("wrote %s\nnext steps: publish it to your Multica workspace (Settings -> Plugins\nor POST /api/workspaces/{id}/plugins/packages), install it, then rotate\nthe plugin token to obtain the signing secret.\n", out)
			return nil
		},
	}
	cmd.Flags().StringVar(&bridgeHost, "bridge-host", "",
		"host (or IP) the bridge is reachable at from Multica (required)")
	cmd.Flags().StringVar(&out, "out", "multica.plugin.json", "output path")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}
