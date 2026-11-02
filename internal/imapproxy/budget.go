package imapproxy

import "sync"

// budget tracks concurrent proxy sessions per account.
type budget struct {
	mu    sync.Mutex
	max   int
	count map[string]int
}

func newBudget(max int) *budget {
	if max <= 0 {
		max = 2
	}
	return &budget{max: max, count: map[string]int{}}
}

func (b *budget) acquire(accountID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count[accountID] >= b.max {
		return false
	}
	b.count[accountID]++
	return true
}

func (b *budget) release(accountID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.count[accountID] - 1
	if n <= 0 {
		delete(b.count, accountID)
		return
	}
	b.count[accountID] = n
}

func (b *budget) active(accountID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count[accountID]
}
