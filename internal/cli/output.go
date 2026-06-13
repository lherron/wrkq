package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/spf13/cobra"
)

type outputMode string

const (
	outputModeTable  outputMode = "table"
	outputModeHuman  outputMode = "human"
	outputModeJSON   outputMode = "json"
	outputModeNDJSON outputMode = "ndjson"
	outputModeYAML   outputMode = "yaml"
	outputModeTSV    outputMode = "tsv"
	outputModeRaw    outputMode = "raw"
)

type outputShape string

const (
	outputShapeStream    outputShape = "stream"
	outputShapeList      outputShape = "list"
	outputShapeSingleton outputShape = "singleton"
	outputShapeContent   outputShape = "content"
	outputShapeMutation  outputShape = "mutation"
)

type outputResolveOptions struct {
	Allow               []outputMode
	DefaultTTY          outputMode
	DefaultTTYStable    bool
	DefaultNonTTY       outputMode
	DefaultNonTTYStable bool
}

type outputSelection struct {
	Mode   outputMode
	Stable bool
}

func resolveOutputMode(cmd *cobra.Command, cfg *config.Config, shape outputShape, opts outputResolveOptions) (outputSelection, error) {
	allowed := outputAllowedSet(opts.Allow)

	explicit, stable, count, err := explicitOutputFlagMode(cmd)
	if err != nil {
		return outputSelection{}, err
	}
	if count > 1 {
		return outputSelection{}, fmt.Errorf("choose only one output mode")
	}
	if explicit != "" {
		mode, err := requireAllowedOutput(explicit, allowed)
		return outputSelection{Mode: mode, Stable: stable}, err
	}
	if stable {
		mode, err := requireAllowedOutput(canonicalMachineOutputMode(shape), allowed)
		return outputSelection{Mode: mode, Stable: true}, err
	}

	if outputFlag := cmd.Flag("output"); outputFlag != nil && outputFlag.Changed {
		mode, stable, err := parseOutputMode(outputFlag.Value.String(), shape)
		if err != nil {
			return outputSelection{}, err
		}
		mode, err = requireAllowedOutput(mode, allowed)
		return outputSelection{Mode: mode, Stable: stable}, err
	}

	if cfg != nil && strings.TrimSpace(cfg.Output) != "" {
		mode, stable, err := parseOutputMode(cfg.Output, shape)
		if err != nil {
			return outputSelection{}, err
		}
		mode, err = requireAllowedOutput(mode, allowed)
		return outputSelection{Mode: mode, Stable: stable}, err
	}

	if isStdoutTTY(cmd.OutOrStdout()) {
		if opts.DefaultTTY != "" {
			mode, err := requireAllowedOutput(opts.DefaultTTY, allowed)
			return outputSelection{Mode: mode, Stable: opts.DefaultTTYStable}, err
		}
		mode, stable := defaultTTYOutputMode(shape)
		mode, err := requireAllowedOutput(mode, allowed)
		return outputSelection{Mode: mode, Stable: stable}, err
	}
	if opts.DefaultNonTTY != "" {
		mode, err := requireAllowedOutput(opts.DefaultNonTTY, allowed)
		return outputSelection{Mode: mode, Stable: opts.DefaultNonTTYStable}, err
	}
	mode, err := requireAllowedOutput(defaultNonTTYOutputMode(shape), allowed)
	return outputSelection{Mode: mode}, err
}

func explicitOutputFlagMode(cmd *cobra.Command) (outputMode, bool, int, error) {
	stable := false
	if f := cmd.Flag("porcelain"); f != nil {
		v, err := cmd.Flags().GetBool("porcelain")
		if err != nil {
			return "", false, 0, err
		}
		stable = v
	}

	candidates := []struct {
		flag string
		mode outputMode
	}{
		{"json", outputModeJSON},
		{"ndjson", outputModeNDJSON},
		{"human", outputModeHuman},
		{"yaml", outputModeYAML},
		{"tsv", outputModeTSV},
	}

	var selected outputMode
	count := 0
	for _, c := range candidates {
		f := cmd.Flag(c.flag)
		if f == nil {
			continue
		}
		v, err := cmd.Flags().GetBool(c.flag)
		if err != nil {
			return "", false, 0, err
		}
		if !v {
			continue
		}
		selected = c.mode
		count++
	}
	return selected, stable, count, nil
}

func parseOutputMode(raw string, shape outputShape) (outputMode, bool, error) {
	mode := outputMode(strings.ToLower(strings.TrimSpace(raw)))
	switch mode {
	case outputModeTable, outputModeHuman, outputModeJSON, outputModeNDJSON,
		outputModeYAML, outputModeTSV, outputModeRaw:
		return mode, false, nil
	case "porcelain":
		return canonicalMachineOutputMode(shape), true, nil
	default:
		return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", raw)
	}
}

func outputAllowedSet(modes []outputMode) map[outputMode]bool {
	if len(modes) == 0 {
		return map[outputMode]bool{
			outputModeTable: true, outputModeHuman: true, outputModeJSON: true,
			outputModeNDJSON: true, outputModeYAML: true,
			outputModeTSV: true, outputModeRaw: true,
		}
	}
	allowed := make(map[outputMode]bool, len(modes))
	for _, mode := range modes {
		allowed[mode] = true
	}
	return allowed
}

func requireAllowedOutput(mode outputMode, allowed map[outputMode]bool) (outputMode, error) {
	if allowed[mode] {
		return mode, nil
	}
	return "", fmt.Errorf("output mode %q is not supported for this command", mode)
}

func canonicalMachineOutputMode(shape outputShape) outputMode {
	switch shape {
	case outputShapeStream, outputShapeList, outputShapeSingleton, outputShapeMutation:
		if shape == outputShapeStream || shape == outputShapeList {
			return outputModeNDJSON
		}
		return outputModeJSON
	default:
		return outputModeRaw
	}
}

func defaultTTYOutputMode(shape outputShape) (outputMode, bool) {
	switch shape {
	case outputShapeStream, outputShapeList:
		return outputModeNDJSON, true
	case outputShapeContent:
		return outputModeRaw, false
	case outputShapeSingleton, outputShapeMutation:
		return outputModeJSON, true
	default:
		return outputModeHuman, false
	}
}

func defaultNonTTYOutputMode(shape outputShape) outputMode {
	switch shape {
	case outputShapeStream, outputShapeList:
		return outputModeNDJSON
	case outputShapeContent:
		return outputModeRaw
	default:
		return outputModeJSON
	}
}

func writeJSONOutput(w io.Writer, sel outputSelection, v interface{}) error {
	enc := json.NewEncoder(w)
	if !sel.Stable {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

func writeNDJSONOutput(w io.Writer, items interface{}) error {
	enc := json.NewEncoder(w)
	value := reflect.ValueOf(items)
	if value.IsValid() && (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) {
		for i := 0; i < value.Len(); i++ {
			if err := enc.Encode(value.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	return enc.Encode(items)
}
