package bulk

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSequentialExecution(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	executed := []string{}
	var mu sync.Mutex

	op := &Operation{
		Jobs:            1,
		ContinueOnError: false,
	}

	fn := func(item string) error {
		mu.Lock()
		executed = append(executed, item)
		mu.Unlock()
		return nil
	}

	result := op.Execute(items, fn)

	if result.TotalItems != 5 {
		t.Errorf("Expected 5 total items, got %d", result.TotalItems)
	}
	if result.Succeeded != 5 {
		t.Errorf("Expected 5 successes, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Expected 0 failures, got %d", result.Failed)
	}

	// Check order is preserved
	for i, item := range items {
		if executed[i] != item {
			t.Errorf("Order not preserved: expected %s at index %d, got %s", item, i, executed[i])
		}
	}
}

func TestParallelExecution(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	executedMap := make(map[string]bool)
	var mu sync.Mutex

	op := &Operation{
		Jobs:            4,
		ContinueOnError: false,
	}

	fn := func(item string) error {
		mu.Lock()
		executedMap[item] = true
		mu.Unlock()
		time.Sleep(10 * time.Millisecond) // Simulate work
		return nil
	}

	result := op.Execute(items, fn)

	if result.TotalItems != 8 {
		t.Errorf("Expected 8 total items, got %d", result.TotalItems)
	}
	if result.Succeeded != 8 {
		t.Errorf("Expected 8 successes, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Expected 0 failures, got %d", result.Failed)
	}

	// Check all items were executed
	for _, item := range items {
		if !executedMap[item] {
			t.Errorf("Item %s was not executed", item)
		}
	}
}

func TestContinueOnError(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}

	op := &Operation{
		Jobs:            1,
		ContinueOnError: true,
	}

	fn := func(item string) error {
		if item == "c" {
			return errors.New("simulated error")
		}
		return nil
	}

	result := op.Execute(items, fn)

	if result.TotalItems != 5 {
		t.Errorf("Expected 5 total items, got %d", result.TotalItems)
	}
	if result.Succeeded != 4 {
		t.Errorf("Expected 4 successes, got %d", result.Succeeded)
	}
	if result.Failed != 1 {
		t.Errorf("Expected 1 failure, got %d", result.Failed)
	}
	if len(result.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0].Item != "c" {
		t.Errorf("Expected error for item 'c', got '%s'", result.Errors[0].Item)
	}
}

func TestStopOnError(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	executed := []string{}
	var mu sync.Mutex

	op := &Operation{
		Jobs:            1,
		ContinueOnError: false,
	}

	fn := func(item string) error {
		mu.Lock()
		executed = append(executed, item)
		mu.Unlock()

		if item == "c" {
			return errors.New("simulated error")
		}
		return nil
	}

	result := op.Execute(items, fn)

	if result.Succeeded != 2 {
		t.Errorf("Expected 2 successes, got %d", result.Succeeded)
	}
	if result.Failed != 1 {
		t.Errorf("Expected 1 failure, got %d", result.Failed)
	}
	if len(executed) != 3 {
		t.Errorf("Expected execution to stop after 3 items, got %d", len(executed))
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name     string
		result   *Result
		expected int
	}{
		{
			name: "all succeeded",
			result: &Result{
				TotalItems: 10,
				Succeeded:  10,
				Failed:     0,
			},
			expected: 0,
		},
		{
			name: "partial success",
			result: &Result{
				TotalItems: 10,
				Succeeded:  7,
				Failed:     3,
			},
			expected: 5,
		},
		{
			name: "all failed",
			result: &Result{
				TotalItems: 10,
				Succeeded:  0,
				Failed:     10,
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := tt.result.ExitCode()
			if code != tt.expected {
				t.Errorf("Expected exit code %d, got %d", tt.expected, code)
			}
		})
	}
}

func TestEmptyItems(t *testing.T) {
	items := []string{}

	op := &Operation{
		Jobs: 4,
	}

	fn := func(item string) error {
		return nil
	}

	result := op.Execute(items, fn)

	if result.TotalItems != 0 {
		t.Errorf("Expected 0 total items, got %d", result.TotalItems)
	}
	if result.Succeeded != 0 {
		t.Errorf("Expected 0 successes, got %d", result.Succeeded)
	}
}

func TestAutoCPUDetection(t *testing.T) {
	items := []string{"a", "b", "c", "d"}

	op := &Operation{
		Jobs: 0, // Should auto-detect
	}

	fn := func(item string) error {
		return nil
	}

	result := op.Execute(items, fn)

	if result.Succeeded != 4 {
		t.Errorf("Expected 4 successes, got %d", result.Succeeded)
	}
}
