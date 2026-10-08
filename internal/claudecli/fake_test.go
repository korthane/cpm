package claudecli

import (
	"sync"
	"testing"
)

func TestFakeRunnerRecordsConcurrentCalls(t *testing.T) {
	f := &FakeRunner{}
	const n = 64

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, _ = f.Run(t.Context(), "/d", "plugin", "marketplace", "update")
		})
	}
	wg.Wait()

	if len(f.Calls) != n {
		t.Errorf("calls = %d, want %d", len(f.Calls), n)
	}
}
