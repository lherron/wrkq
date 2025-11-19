package domain

import (
	"reflect"
	"testing"
)

func TestTask_GetLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  *string
		want    []string
		wantErr bool
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   []string{},
		},
		{
			name:   "empty string",
			labels: stringPtr(""),
			want:   []string{},
		},
		{
			name:   "single label",
			labels: stringPtr(`["backend"]`),
			want:   []string{"backend"},
		},
		{
			name:   "multiple labels",
			labels: stringPtr(`["backend","frontend","api"]`),
			want:   []string{"backend", "frontend", "api"},
		},
		{
			name:   "empty array",
			labels: stringPtr(`[]`),
			want:   []string{},
		},
		{
			name:    "invalid JSON",
			labels:  stringPtr(`not-json`),
			wantErr: true,
		},
		{
			name:    "invalid JSON array",
			labels:  stringPtr(`["unclosed`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{Labels: tt.labels}
			got, err := task.GetLabels()
			if tt.wantErr {
				if err == nil {
					t.Error("GetLabels() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("GetLabels() unexpected error: %v", err)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTask_SetLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   string
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   "[]",
		},
		{
			name:   "empty slice",
			labels: []string{},
			want:   "[]",
		},
		{
			name:   "single label",
			labels: []string{"backend"},
			want:   `["backend"]`,
		},
		{
			name:   "multiple labels",
			labels: []string{"backend", "frontend", "api"},
			want:   `["backend","frontend","api"]`,
		},
		{
			name:   "labels with special characters",
			labels: []string{"high-priority", "bug/fix"},
			want:   `["high-priority","bug/fix"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{}
			err := task.SetLabels(tt.labels)
			if err != nil {
				t.Errorf("SetLabels() unexpected error: %v", err)
				return
			}
			if task.Labels == nil {
				t.Error("SetLabels() labels is nil")
				return
			}
			if *task.Labels != tt.want {
				t.Errorf("SetLabels() = %q, want %q", *task.Labels, tt.want)
			}
		})
	}
}

func TestTask_GetSetLabelsRoundtrip(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
	}{
		{
			name:   "empty",
			labels: []string{},
		},
		{
			name:   "single",
			labels: []string{"backend"},
		},
		{
			name:   "multiple",
			labels: []string{"backend", "frontend", "api", "urgent"},
		},
		{
			name:   "with special chars",
			labels: []string{"high-priority", "bug/fix", "v2.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{}

			// Set labels
			if err := task.SetLabels(tt.labels); err != nil {
				t.Fatalf("SetLabels() error: %v", err)
			}

			// Get labels back
			got, err := task.GetLabels()
			if err != nil {
				t.Fatalf("GetLabels() error: %v", err)
			}

			// Compare
			if !reflect.DeepEqual(got, tt.labels) {
				t.Errorf("Roundtrip failed: got %v, want %v", got, tt.labels)
			}
		})
	}
}

// Helper function
func stringPtr(s string) *string {
	return &s
}

// Benchmark tests
func BenchmarkTask_GetLabels(b *testing.B) {
	labels := stringPtr(`["backend","frontend","api","urgent","high-priority"]`)
	task := &Task{Labels: labels}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		task.GetLabels()
	}
}

func BenchmarkTask_SetLabels(b *testing.B) {
	labels := []string{"backend", "frontend", "api", "urgent", "high-priority"}
	task := &Task{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		task.SetLabels(labels)
	}
}
