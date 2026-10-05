package wrkfcli

import (
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func workflowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workflow", Short: "Validate, install, and inspect workflow templates"}
	validate := &cobra.Command{
		Use:   "validate TEMPLATE",
		Short: "Validate a workflow template file",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			body, err := readTemplateBody(args[0])
			if err != nil {
				return err
			}
			result, err := rpcCall[wrkfapi.ValidateResult](cmd, a, "wrkf.workflow.validate", wrkfapi.WorkflowContentParams{Body: body, SourceName: args[0]})
			if err != nil {
				return err
			}
			if flagJSON {
				_ = printJSON(cmd, result)
			} else if result.Valid {
				cmd.Printf("valid %s@%s %s\n", result.ID, result.Version, result.Hash)
			} else {
				cmd.Printf("invalid\n")
				for _, e := range result.Errors {
					cmd.Printf("- %s\n", e)
				}
			}
			if !result.Valid {
				return fmt.Errorf("template validation failed")
			}
			return nil
		}),
	}
	install := &cobra.Command{
		Use:   "install TEMPLATE",
		Short: "Install a workflow template file",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			body, err := readTemplateBody(args[0])
			if err != nil {
				return err
			}
			out, err := rpcCall[wrkfapi.InstallResult](cmd, a, "wrkf.workflow.install", wrkfapi.WorkflowInstallParams{
				Body: body, SourceName: args[0], PrincipalRef: a.principalRef,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]any{"id": out.ID, "version": out.Version, "hash": out.Hash, "installed": out.Installed})
		}),
	}
	show := &cobra.Command{
		Use:   "show ID@VERSION",
		Short: "Show an installed workflow template version",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			info, err := rpcCall[wrkfapi.WorkflowShowResult](cmd, a, "wrkf.workflow.show", map[string]any{"ref": args[0]})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, info)
			}
			marker := ""
			if info.DiscontinuedAt != "" {
				marker = fmt.Sprintf(" DISCONTINUED at %s", info.DiscontinuedAt)
				if info.DiscontinuedBy != "" {
					marker += " by " + info.DiscontinuedBy
				}
			}
			cmd.Printf("%s@%s %s%s\n", info.Template.ID, info.Template.Version, info.Hash, marker)
			return nil
		}),
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List installed workflow templates",
		Args:  cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.WorkflowListResult](cmd, a, "wrkf.workflow.list", map[string]any{})
			if err != nil {
				return err
			}
			var templates []map[string]interface{}
			for _, template := range result.Templates {
				row := map[string]interface{}{
					"id": template.ID, "version": template.Version, "hash": template.Hash, "installedAt": template.InstalledAt,
				}
				if template.InstalledBy != "" {
					row["installedBy"] = template.InstalledBy
				}
				if template.DiscontinuedAt != "" {
					row["discontinuedAt"] = template.DiscontinuedAt
				}
				if template.DiscontinuedBy != "" {
					row["discontinuedBy"] = template.DiscontinuedBy
				}
				templates = append(templates, row)
			}
			if flagJSON {
				return printJSON(cmd, map[string]interface{}{"templates": templates})
			}
			for _, template := range templates {
				marker := ""
				if _, discontinued := template["discontinuedAt"]; discontinued {
					marker = " DISCONTINUED"
				}
				cmd.Printf("%s@%s %s%s\n", template["id"], template["version"], template["hash"], marker)
			}
			return nil
		}),
	}
	discontinue := templateLifecycleCommand("discontinue")
	reinstate := templateLifecycleCommand("reinstate")
	diff := &cobra.Command{
		Use:   "diff OLD NEW",
		Short: "Diff two workflow template versions",
		Args:  cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			oldBody, err := readTemplateBody(args[0])
			if err != nil {
				return err
			}
			newBody, err := readTemplateBody(args[1])
			if err != nil {
				return err
			}
			if len(oldBody)+len(newBody) > wrkfapi.MaxTemplateDiffBodyBytes {
				return fmt.Errorf("template diff bodies exceed %d-byte aggregate limit", wrkfapi.MaxTemplateDiffBodyBytes)
			}
			out, err := rpcCall[wrkfapi.DiffResult](cmd, a, "wrkf.workflow.diff", wrkfapi.WorkflowDiffParams{
				OldBody: oldBody, NewBody: newBody, OldSourceName: args[0], NewSourceName: args[1],
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.AddCommand(validate, install, show, list, diff, discontinue, reinstate)
	return cmd
}

func readTemplateBody(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > wrkfapi.MaxTemplateBodyBytes {
		return "", fmt.Errorf("%s exceeds %d-byte template body limit", path, wrkfapi.MaxTemplateBodyBytes)
	}
	return string(data), nil
}

func templateLifecycleCommand(verb string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " ID@VERSION",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a workflow template version",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			info, err := rpcCall[wrkfapi.WorkflowShowResult](cmd, a, "wrkf.workflow."+verb, map[string]any{
				"ref": args[0], "principal_ref": a.principalRef,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, info)
			}
			cmd.Printf("%s %s\n", verb, args[0])
			return nil
		}),
	}
}
