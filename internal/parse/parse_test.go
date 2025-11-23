package parse

import (
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Format
		wantErr bool
	}{
		{
			name:    "empty input defaults to markdown",
			input:   "",
			want:    FormatMarkdown,
			wantErr: false,
		},
		{
			name:    "valid JSON object",
			input:   `{"title": "test"}`,
			want:    FormatJSON,
			wantErr: false,
		},
		{
			name:    "valid JSON array",
			input:   `[1, 2, 3]`,
			want:    FormatJSON,
			wantErr: false,
		},
		{
			name:    "invalid JSON returns error",
			input:   `{not valid json}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "markdown with front matter",
			input:   "---\ntitle: test\n---\nContent",
			want:    FormatMarkdown,
			wantErr: false,
		},
		{
			name: "YAML structure",
			input: `title: test
state: open
priority: 1`,
			want:    FormatYAML,
			wantErr: false,
		},
		{
			name:    "plain text defaults to markdown",
			input:   "Just some plain text description",
			want:    FormatMarkdown,
			wantErr: false,
		},
		{
			name:    "whitespace only defaults to markdown",
			input:   "   \n\n  ",
			want:    FormatMarkdown,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectFormat([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("DetectFormat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DetectFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*testing.T, *TaskUpdate)
	}{
		{
			name:    "valid JSON with all fields",
			input:   `{"title": "Test", "state": "open", "priority": 1, "description": "Desc"}`,
			wantErr: false,
			check: func(t *testing.T, u *TaskUpdate) {
				if u.Title == nil || *u.Title != "Test" {
					t.Errorf("title = %v, want Test", u.Title)
				}
				if u.State == nil || *u.State != "open" {
					t.Errorf("state = %v, want open", u.State)
				}
			},
		},
		{
			name:    "invalid JSON",
			input:   `{not json}`,
			wantErr: true,
		},
		{
			name:    "empty object",
			input:   `{}`,
			wantErr: false,
			check: func(t *testing.T, u *TaskUpdate) {
				if u.Title != nil || u.State != nil || u.Description != nil {
					t.Errorf("expected all fields nil for empty object")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseJSON([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestParseYAML(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*testing.T, *TaskUpdate)
	}{
		{
			name: "valid YAML with all fields",
			input: `title: Test Task
state: open
priority: 1
description: Test description`,
			wantErr: false,
			check: func(t *testing.T, u *TaskUpdate) {
				if u.Title == nil || *u.Title != "Test Task" {
					t.Errorf("title = %v, want Test Task", u.Title)
				}
				if u.State == nil || *u.State != "open" {
					t.Errorf("state = %v, want open", u.State)
				}
			},
		},
		{
			name:    "invalid YAML",
			input:   ": invalid yaml structure",
			wantErr: true,
		},
		{
			name:    "empty YAML",
			input:   "",
			wantErr: false,
			check: func(t *testing.T, u *TaskUpdate) {
				if u.Title != nil || u.State != nil {
					t.Errorf("expected all fields nil for empty YAML")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseYAML([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestParseMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*testing.T, *TaskUpdate)
	}{
		{
			name:    "plain markdown no front matter",
			input:   "This is a description\n\nWith multiple lines",
			wantErr: false,
			check: func(t *testing.T, u *TaskUpdate) {
				if u.Description == nil {
					t.Fatal("description is nil")
				}
				if *u.Description != "This is a description\n\nWith multiple lines" {
					t.Errorf("description = %v", *u.Description)
				}
			},
		},
		{
			name: "markdown with YAML front matter",
			input: `---
title: Test
state: open
---

Description content here`,
			wantErr: false,
			check: func(t *testing.T, u *TaskUpdate) {
				if u.Title == nil || *u.Title != "Test" {
					t.Errorf("title = %v, want Test", u.Title)
				}
				if u.Description == nil || *u.Description != "Description content here" {
					t.Errorf("description = %v", u.Description)
				}
			},
		},
		{
			name:    "invalid front matter format",
			input:   "---\ntitle: test\n\nno closing delimiter",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMarkdown([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMarkdown() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		format     string
		wantErr    bool
		wantFormat Format
	}{
		{
			name:       "auto-detect JSON",
			input:      `{"title": "test"}`,
			format:     "",
			wantErr:    false,
			wantFormat: FormatJSON,
		},
		{
			name:       "auto-detect markdown",
			input:      "Plain text description",
			format:     "",
			wantErr:    false,
			wantFormat: FormatMarkdown,
		},
		{
			name:       "explicit JSON format",
			input:      `{"title": "test"}`,
			format:     "json",
			wantErr:    false,
			wantFormat: FormatJSON,
		},
		{
			name:    "invalid auto-detected JSON",
			input:   `{bad json}`,
			format:  "",
			wantErr: true,
		},
		{
			name:    "unsupported format",
			input:   "content",
			format:  "xml",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input), tt.format)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
