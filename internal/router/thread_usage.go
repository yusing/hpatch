package router

import "sync"

// Thread totals are auxiliary, independent of routing sessions and replay history.
// Existing identities are never evicted: an untracked thread must not later show a
// partial lifetime total as though it were complete.
type threadUsage struct {
	mu      sync.Mutex
	threads map[string]*threadUsageTotal
	closed  bool
}

type threadUsageTotal struct {
	counts   tokenCounts
	complete bool
}

type threadUsageObservation struct {
	once       sync.Once
	totals     *threadUsage
	conflicted bool
	thread     string
}

func newThreadUsage() *threadUsage {
	return &threadUsage{threads: make(map[string]*threadUsageTotal)}
}

// Transport identity owns usage accounting, not auxiliary author or ancestry
// metadata. A contradictory explicit thread ID makes lifetime totals incomplete.
func (u *threadUsage) observation(thread, metadataThread string) *threadUsageObservation {
	return &threadUsageObservation{totals: u, thread: thread, conflicted: metadataThread != "" && metadataThread != thread}
}

func (o *threadUsageObservation) observe(counts tokenCounts) {
	if o == nil {
		return
	}
	o.once.Do(func() { o.totals.add(o.thread, counts, o.conflicted) })
}

func (u *threadUsage) add(thread string, counts tokenCounts, conflicted bool) {
	if u == nil || thread == "" || len(thread) > maxCommentaryPublicationBytes {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	total := u.threads[thread]
	if total == nil {
		if len(u.threads) >= maxCommentaryRoutes {
			return
		}
		total = &threadUsageTotal{complete: true}
		u.threads[thread] = total
	}
	if conflicted {
		total.complete = false
	}
	if !total.complete {
		return
	}
	sum := total.counts
	for _, pair := range []struct {
		dst *uint64
		add uint64
	}{
		{&sum.InputTokens, counts.InputTokens},
		{&sum.UncachedInputTokens, counts.UncachedInputTokens},
		{&sum.OutputTokens, counts.OutputTokens},
		{&sum.ReasoningTokens, counts.ReasoningTokens},
	} {
		if ^uint64(0)-*pair.dst < pair.add {
			total.complete = false
			return
		}
		*pair.dst += pair.add
	}
	total.counts = sum
}

func (u *threadUsage) snapshot(thread string) (tokenCounts, bool) {
	if u == nil {
		return tokenCounts{}, false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if total := u.threads[thread]; !u.closed && total != nil && total.complete {
		return total.counts, true
	}
	return tokenCounts{}, false
}

func (u *threadUsage) close() {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	clear(u.threads)
}

func (t *hpatchResponseTransform) threadUsageCounts() (tokenCounts, bool) {
	if t.usageTracker == nil {
		return tokenCounts{}, false
	}
	return t.usageTracker.totals.snapshot(t.usageTracker.thread)
}
