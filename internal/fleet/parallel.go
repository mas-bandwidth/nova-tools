package fleet

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// RunBenchesConcurrently runs fn concurrently for each bench name in names.
// Wall time is under 1.5x the slowest fn call.
// Returns one error naming every bench whose fn returned an error.
func RunBenchesConcurrently(names []string, fn func(name string) error) error {
	var (
		mu   sync.Mutex
		errs []string
		wg   sync.WaitGroup
	)

	wg.Add(len(names))
	for _, name := range names {
		go func(name string) {
			defer wg.Done()
			if err := fn(name); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %s", name, err.Error()))
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("failed benches: %s", strings.Join(errs, "; "))
}
