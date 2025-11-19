package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format represents an output format
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatNDJSON Format = "ndjson"
	FormatYAML  Format = "yaml"
	FormatTSV   Format = "tsv"
)

// Options for rendering
type Options struct {
	Format     Format
	Porcelain  bool
	Fields     []string
	Delimiter  string // for -1 (newline) or -0 (NUL)
}

// Renderer handles output rendering
type Renderer struct {
	writer io.Writer
	opts   Options
}

// NewRenderer creates a new renderer
func NewRenderer(writer io.Writer, opts Options) *Renderer {
	return &Renderer{
		writer: writer,
		opts:   opts,
	}
}

// RenderJSON renders data as JSON
func (r *Renderer) RenderJSON(data interface{}) error {
	encoder := json.NewEncoder(r.writer)
	if !r.opts.Porcelain {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(data)
}

// RenderNDJSON renders data as newline-delimited JSON
func (r *Renderer) RenderNDJSON(items []interface{}) error {
	encoder := json.NewEncoder(r.writer)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

// RenderYAML renders data as YAML
func (r *Renderer) RenderYAML(data interface{}) error {
	encoder := yaml.NewEncoder(r.writer)
	defer encoder.Close()
	return encoder.Encode(data)
}

// RenderTSV renders data as tab-separated values
func (r *Renderer) RenderTSV(headers []string, rows [][]string) error {
	// Write header
	if _, err := fmt.Fprintln(r.writer, strings.Join(headers, "\t")); err != nil {
		return err
	}

	// Write rows
	for _, row := range rows {
		if _, err := fmt.Fprintln(r.writer, strings.Join(row, "\t")); err != nil {
			return err
		}
	}

	return nil
}

// RenderList renders a simple list of strings
func (r *Renderer) RenderList(items []string) error {
	delimiter := "\n"
	if r.opts.Delimiter == "0" {
		delimiter = "\x00"
	}

	for i, item := range items {
		if _, err := fmt.Fprint(r.writer, item); err != nil {
			return err
		}
		if i < len(items)-1 || r.opts.Delimiter != "" {
			if _, err := fmt.Fprint(r.writer, delimiter); err != nil {
				return err
			}
		}
	}

	return nil
}

// RenderTable renders data as a formatted table
func (r *Renderer) RenderTable(headers []string, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}

	// Calculate column widths
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}

	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Render header
	if !r.opts.Porcelain {
		r.renderTableRow(headers, widths)
		r.renderTableSeparator(widths)
	} else {
		// Porcelain mode: just tab-separated
		fmt.Fprintln(r.writer, strings.Join(headers, "\t"))
	}

	// Render rows
	for _, row := range rows {
		if r.opts.Porcelain {
			fmt.Fprintln(r.writer, strings.Join(row, "\t"))
		} else {
			r.renderTableRow(row, widths)
		}
	}

	return nil
}

func (r *Renderer) renderTableRow(cells []string, widths []int) {
	for i, cell := range cells {
		if i < len(widths) {
			fmt.Fprintf(r.writer, "%-*s", widths[i], cell)
			if i < len(cells)-1 {
				fmt.Fprint(r.writer, "  ")
			}
		}
	}
	fmt.Fprintln(r.writer)
}

func (r *Renderer) renderTableSeparator(widths []int) {
	for i, width := range widths {
		fmt.Fprint(r.writer, strings.Repeat("-", width))
		if i < len(widths)-1 {
			fmt.Fprint(r.writer, "  ")
		}
	}
	fmt.Fprintln(r.writer)
}

// Package-level helper functions for simple rendering

// RenderJSON renders data as pretty JSON to stdout
func RenderJSON(data interface{}, compact bool) error {
	encoder := json.NewEncoder(os.Stdout)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(data)
}

// RenderNDJSON renders items as newline-delimited JSON to stdout
func RenderNDJSON(items interface{}) error {
	encoder := json.NewEncoder(os.Stdout)

	// Handle slice types
	switch v := items.(type) {
	case []interface{}:
		for _, item := range v {
			if err := encoder.Encode(item); err != nil {
				return err
			}
		}
	default:
		// Try to encode as a single item
		return encoder.Encode(items)
	}

	return nil
}

// RenderNulSeparated renders items with NUL separators
func RenderNulSeparated(items interface{}) error {
	// Extract path or ID from items
	type pathProvider interface {
		GetPath() string
	}

	switch v := items.(type) {
	case []interface{}:
		for _, item := range v {
			// Try to get path from item
			if pp, ok := item.(pathProvider); ok {
				fmt.Print(pp.GetPath())
			} else {
				// Fall back to JSON representation
				b, _ := json.Marshal(item)
				fmt.Print(string(b))
			}
			fmt.Print("\x00")
		}
	default:
		b, err := json.Marshal(items)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		fmt.Print("\x00")
	}

	return nil
}

// Extractor for getting field from struct
type fieldExtractor func(interface{}) string

// RenderTable renders a slice of structs as a table
func RenderTable(items interface{}, porcelain bool) error {
	// This is a generic table renderer
	// For now, use JSON as fallback
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}
