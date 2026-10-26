package bulk

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestEveryItemRunsOnceAtAnyJobCount: Execute runs each item exactly once and
// counts it as a success whether it runs sequentially (Jobs=1, input order
// kept), on a worker pool (Jobs=4), or on an auto-sized pool (Jobs=0 means
// NumCPU workers; zero workers would hang or run nothing).
func TestEveryItemRunsOnceAtAnyJobCount(t *testing.T) {
	tests := []struct {
		name        string
		jobs        int
		items       []string
		wantOrdered bool
	}{
		{name: "sequential keeps input order", jobs: 1, items: []string{"a", "b", "c", "d", "e"}, wantOrdered: true},
		{name: "parallel worker pool", jobs: 4, items: []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
		{name: "jobs=0 auto-sizes to NumCPU", jobs: 0, items: []string{"a", "b", "c", "d"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			executed := []string{}
			fn := func(item string) error {
				mu.Lock()
				executed = append(executed, item)
				mu.Unlock()
				if tt.jobs != 1 {
					time.Sleep(10 * time.Millisecond) // overlap the workers
				}
				return nil
			}

			result := (&Operation{Jobs: tt.jobs}).Execute(tt.items, fn)

			n := len(tt.items)
			if result.TotalItems != n || result.Succeeded != n || result.Failed != 0 {
				t.Fatalf("result = total %d / ok %d / failed %d, want %d / %d / 0", result.TotalItems, result.Succeeded, result.Failed, n, n)
			}
			if len(executed) != n {
				t.Fatalf("executed %d items (%v), want each of %d exactly once", len(executed), executed, n)
			}
			seen := map[string]int{}
			for _, item := range executed {
				seen[item]++
			}
			for i, item := range tt.items {
				if seen[item] != 1 {
					t.Errorf("item %s ran %d times, want 1", item, seen[item])
				}
				if tt.wantOrdered && executed[i] != item {
					t.Errorf("order not preserved: index %d = %s, want %s", i, executed[i], item)
				}
			}
		})
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
