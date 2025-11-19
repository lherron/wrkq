package bulk

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

// Operation represents a bulk operation configuration
type Operation struct {
	Jobs            int
	BatchSize       int
	ContinueOnError bool
	Ordered         bool
	ShowProgress    bool
}

// Result represents the result of a bulk operation
type Result struct {
	TotalItems int
	Succeeded  int
	Failed     int
	Errors     []ItemError
}

// ItemError represents an error for a specific item
type ItemError struct {
	Item  string
	Error error
}

// ItemFunc is the function to execute for each item
type ItemFunc func(item string) error

// Execute runs the bulk operation on the given items
func (op *Operation) Execute(items []string, fn ItemFunc) *Result {
	result := &Result{
		TotalItems: len(items),
	}

	if len(items) == 0 {
		return result
	}

	// Auto-detect CPU count if jobs == 0
	jobs := op.Jobs
	if jobs == 0 {
		jobs = runtime.NumCPU()
	}

	// Force sequential if ordered or jobs == 1
	if op.Ordered || jobs == 1 {
		return op.executeSequential(items, fn)
	}

	return op.executeParallel(items, fn, jobs)
}

// executeSequential processes items one by one
func (op *Operation) executeSequential(items []string, fn ItemFunc) *Result {
	result := &Result{
		TotalItems: len(items),
	}

	for i, item := range items {
		if op.ShowProgress && isatty(os.Stdout) {
			fmt.Fprintf(os.Stderr, "\rProcessing %d/%d...", i+1, len(items))
		}

		err := fn(item)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ItemError{
				Item:  item,
				Error: err,
			})

			if !op.ContinueOnError {
				// Clear progress line if shown
				if op.ShowProgress && isatty(os.Stdout) {
					fmt.Fprintf(os.Stderr, "\r\033[K")
				}
				return result
			}

			if !isatty(os.Stdout) {
				fmt.Fprintf(os.Stderr, "%s: error: %v\n", item, err)
			}
		} else {
			result.Succeeded++
			if !isatty(os.Stdout) {
				fmt.Fprintf(os.Stderr, "%s: success\n", item)
			}
		}
	}

	// Clear progress line
	if op.ShowProgress && isatty(os.Stdout) {
		fmt.Fprintf(os.Stderr, "\r\033[K")
	}

	return result
}

// executeParallel processes items in parallel using a worker pool
func (op *Operation) executeParallel(items []string, fn ItemFunc, workers int) *Result {
	result := &Result{
		TotalItems: len(items),
	}

	// Create work queue
	workQueue := make(chan string, len(items))
	for _, item := range items {
		workQueue <- item
	}
	close(workQueue)

	// Track progress
	var (
		completed  int32
		succeeded  int32
		failed     int32
		errorsMux  sync.Mutex
		stopSignal int32 // 0 = continue, 1 = stop
	)

	// Progress reporter
	var progressDone chan struct{}
	if op.ShowProgress && isatty(os.Stdout) {
		progressDone = make(chan struct{})
		go func() {
			defer close(progressDone)
			for {
				select {
				case <-progressDone:
					return
				default:
					c := atomic.LoadInt32(&completed)
					s := atomic.LoadInt32(&succeeded)
					f := atomic.LoadInt32(&failed)
					pct := int(float64(c) / float64(len(items)) * 100)

					// Simple progress bar
					bar := progressBar(pct, 20)
					fmt.Fprintf(os.Stderr, "\rProcessing with %d workers... [%s] %d/%d (✓ %d ✗ %d)",
						workers, bar, c, len(items), s, f)
				}
			}
		}()
	}

	// Worker pool
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for item := range workQueue {
				// Check if we should stop
				if !op.ContinueOnError && atomic.LoadInt32(&stopSignal) == 1 {
					break
				}

				err := fn(item)
				atomic.AddInt32(&completed, 1)

				if err != nil {
					atomic.AddInt32(&failed, 1)
					errorsMux.Lock()
					result.Errors = append(result.Errors, ItemError{
						Item:  item,
						Error: err,
					})
					errorsMux.Unlock()

					if !op.ContinueOnError {
						atomic.StoreInt32(&stopSignal, 1)
					}

					if !isatty(os.Stdout) {
						fmt.Fprintf(os.Stderr, "%s: error: %v\n", item, err)
					}
				} else {
					atomic.AddInt32(&succeeded, 1)
					if !isatty(os.Stdout) {
						fmt.Fprintf(os.Stderr, "%s: success\n", item)
					}
				}
			}
		}()
	}

	wg.Wait()

	// Stop progress reporter
	if progressDone != nil {
		progressDone <- struct{}{}
		<-progressDone
		fmt.Fprintf(os.Stderr, "\r\033[K") // Clear line
	}

	result.Succeeded = int(succeeded)
	result.Failed = int(failed)

	return result
}

// ExitCode returns the appropriate exit code for the result
func (r *Result) ExitCode() int {
	if r.Failed == 0 {
		return 0 // All succeeded
	}
	if r.Succeeded > 0 {
		return 5 // Partial success
	}
	return 1 // All failed
}

// PrintSummary prints a human-readable summary of the result
func (r *Result) PrintSummary(w io.Writer) {
	if r.Failed == 0 {
		fmt.Fprintf(w, "\n✓ All %d operations succeeded\n", r.TotalItems)
	} else if r.Succeeded == 0 {
		fmt.Fprintf(w, "\n✗ All %d operations failed\n", r.TotalItems)
	} else {
		fmt.Fprintf(w, "\n⚠ Partial success: %d succeeded, %d failed (out of %d)\n",
			r.Succeeded, r.Failed, r.TotalItems)
	}

	if len(r.Errors) > 0 && len(r.Errors) <= 10 {
		fmt.Fprintf(w, "\nErrors:\n")
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  %s: %v\n", e.Item, e.Error)
		}
	} else if len(r.Errors) > 10 {
		fmt.Fprintf(w, "\nShowing first 10 errors (of %d):\n", len(r.Errors))
		for _, e := range r.Errors[:10] {
			fmt.Fprintf(w, "  %s: %v\n", e.Item, e.Error)
		}
	}
}

// progressBar creates a simple ASCII progress bar
func progressBar(percent, width int) string {
	filled := percent * width / 100
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	return bar
}

// isatty checks if the file descriptor is a terminal
func isatty(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
