package render

import (
	"encoding/json"
	"fmt"
	"io"
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
