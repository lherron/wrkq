package rpccli

import (
	"encoding/json"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const CLISurfaceManifestSchema = "wrkq.cli-surface-manifest@1"

// CLISurfaceManifest is a stable JSON summary of the public Cobra tree.
type CLISurfaceManifest struct {
	Schema string                 `json:"schema"`
	Root   CLISurfaceManifestNode `json:"root"`
}

type CLISurfaceManifestNode struct {
	Name            string                   `json:"name"`
	Use             string                   `json:"use"`
	Aliases         []string                 `json:"aliases,omitempty"`
	Short           string                   `json:"short,omitempty"`
	Hidden          bool                     `json:"hidden"`
	Available       bool                     `json:"available"`
	Flags           []CLISurfaceManifestFlag `json:"flags,omitempty"`
	PersistentFlags []CLISurfaceManifestFlag `json:"persistent_flags,omitempty"`
	Commands        []CLISurfaceManifestNode `json:"commands,omitempty"`
}

type CLISurfaceManifestFlag struct {
	Name       string `json:"name"`
	Shorthand  string `json:"shorthand,omitempty"`
	Usage      string `json:"usage,omitempty"`
	Type       string `json:"type,omitempty"`
	Default    string `json:"default,omitempty"`
	Hidden     bool   `json:"hidden"`
	Deprecated string `json:"deprecated,omitempty"`
}

func BuildCLISurfaceManifest(commandName string) CLISurfaceManifest {
	root := NewRootCmdFor(commandName)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	return CLISurfaceManifest{
		Schema: CLISurfaceManifestSchema,
		Root:   buildCLISurfaceManifestNode(root),
	}
}

func BuildCLISurfaceManifestJSON(commandName string) ([]byte, error) {
	manifest := BuildCLISurfaceManifest(commandName)
	return json.MarshalIndent(manifest, "", "  ")
}

func buildCLISurfaceManifestNode(cmd *cobra.Command) CLISurfaceManifestNode {
	node := CLISurfaceManifestNode{
		Name:            cmd.Name(),
		Use:             cmd.Use,
		Aliases:         append([]string(nil), cmd.Aliases...),
		Short:           cmd.Short,
		Hidden:          cmd.Hidden,
		Available:       cmd.IsAvailableCommand(),
		Flags:           manifestFlags(cmd.LocalNonPersistentFlags()),
		PersistentFlags: manifestFlags(cmd.PersistentFlags()),
	}
	for _, child := range cmd.Commands() {
		node.Commands = append(node.Commands, buildCLISurfaceManifestNode(child))
	}
	return node
}

func manifestFlags(flags *pflag.FlagSet) []CLISurfaceManifestFlag {
	if flags == nil {
		return nil
	}
	result := make([]CLISurfaceManifestFlag, 0)
	flags.VisitAll(func(flag *pflag.Flag) {
		result = append(result, CLISurfaceManifestFlag{
			Name:       flag.Name,
			Shorthand:  flag.Shorthand,
			Usage:      flag.Usage,
			Type:       flag.Value.Type(),
			Default:    flag.DefValue,
			Hidden:     flag.Hidden,
			Deprecated: flag.Deprecated,
		})
	})
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}
