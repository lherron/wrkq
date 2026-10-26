package domain

import "testing"

func TestValidatePriority(t *testing.T) {
	tests := []struct {
		name     string
		priority int
		wantErr  bool
	}{
		{name: "priority 1", priority: 1, wantErr: false},
		{name: "priority 2", priority: 2, wantErr: false},
		{name: "priority 3", priority: 3, wantErr: false},
		{name: "priority 4", priority: 4, wantErr: false},
		{name: "too low", priority: 0, wantErr: true},
		{name: "negative", priority: -1, wantErr: true},
		{name: "too high", priority: 5, wantErr: true},
		{name: "way too high", priority: 100, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePriority(tt.priority)
			if tt.wantErr && err == nil {
				t.Error("ValidatePriority() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidatePriority() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateResolution(t *testing.T) {
	tests := []struct {
		name       string
		resolution string
		wantErr    bool
	}{
		{name: "done", resolution: "done", wantErr: false},
		{name: "wont_do", resolution: "wont_do", wantErr: false},
		{name: "duplicate", resolution: "duplicate", wantErr: false},
		{name: "needs_info", resolution: "needs_info", wantErr: false},
		{name: "invalid", resolution: "invalid", wantErr: true},
		{name: "empty", resolution: "", wantErr: true},
		{name: "uppercase", resolution: "DONE", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResolution(tt.resolution)
			if tt.wantErr && err == nil {
				t.Error("ValidateResolution() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateResolution() unexpected error: %v", err)
			}
		})
	}
}
