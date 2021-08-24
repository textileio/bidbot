package limiter

import (
	"container/list"
	"sync"
	"time"
)

// Limiter is the interface for RunningTotalLimiter. It's there just so we can
// have a NopeLimiter which does nothing.
type Limiter interface {
	Request(id string, n uint64, period time.Duration) bool
	Secure(id string)
	Commit(id string)
	Withdraw(id string)
}

// RunningTotalLimiter is a variant of sliding log rate limiter. It keeps all
// requests with timestamps. What makes it different from a typical rate limiter is
// that there are three phases - The caller first requests for pre approval of
// 'n' tokens for a period of time, then secures it before it expires, does
// some work, and finally either commits the tokens, or withdraws them so they
// can be requested by others. So at anytime, the running total represents the
// tokens committed in that period of time, plus those requested but not
// expired or committed yet.
//
// If we are going to have millions of requests, we should switch to a sliding
// window implementation like https://github.com/RussellLuo/slidingwindow,
// which saves space with the cost of some inaccuracy.
type RunningTotalLimiter struct {
	period    time.Duration
	limit     uint64
	total     uint64
	requested map[string]requested
	committed *list.List // committed keeps committed tokens chronologically
	mu        sync.Mutex
}

type requested struct {
	n uint64
	t *time.Timer
}

type elem struct {
	id string
	n  uint64
	ts time.Time
}

// NewRunningTotalLimiter creates a Limiter which caps the running total in the
// 'period' to 'limit'.
func NewRunningTotalLimiter(limit uint64, period time.Duration) *RunningTotalLimiter {
	return &RunningTotalLimiter{period: period, limit: limit, requested: make(map[string]requested), committed: list.New()}
}

// Request reqeusts for n tokens and returns if granted or not. If granted, the
// tokens must be secured before expiration.
func (rl *RunningTotalLimiter) Request(id string, n uint64, expiration time.Duration) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for e := rl.committed.Front(); e != nil; {
		item := e.Value.(elem)
		if now.Sub(item.ts) <= rl.period {
			break
		}
		rl.total -= item.n
		toRemove := e
		e = e.Next()
		rl.committed.Remove(toRemove)
	}
	if rl.total+n > rl.limit {
		return false
	}
	rl.total += n
	rl.requested[id] = requested{
		n: n,
		t: time.AfterFunc(expiration, func() { rl.Withdraw(id) }),
	}
	return true
}

// Secure secures previously requested tokens associated with the id so they
// don't expire automatically.
func (rl *RunningTotalLimiter) Secure(id string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if pa, exists := rl.requested[id]; exists {
		_ = pa.t.Stop()
	}
}

// Commit makes the not-yet-expired or secured tokens associated with the id
// permanent for the configured period.
func (rl *RunningTotalLimiter) Commit(id string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if pa, exists := rl.requested[id]; exists {
		rl.committed.PushBack(elem{id: id, n: pa.n, ts: time.Now()})
		delete(rl.requested, id)
	}
}

// Withdraw withdraws the tokens associated with the id.
func (rl *RunningTotalLimiter) Withdraw(id string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if pa, exists := rl.requested[id]; exists {
		delete(rl.requested, id)
		rl.total -= pa.n
	}
}

// NopeLimiter does no limit.
type NopeLimiter struct{}

// Request always return true.
func (l NopeLimiter) Request(string, uint64, time.Duration) bool { return true }

// Secure does nothing.
func (l NopeLimiter) Secure(string) {}

// Commit does nothing.
func (l NopeLimiter) Commit(string) {}

// Withdraw does nothing.
func (l NopeLimiter) Withdraw(string) {}
