package domain

import "time"

// Clock is the injectable time source. Tightening timestamps must come from a
// monotonic clock so that repeated executions of the deterministic test suite
// produce identical bytes.
type Clock interface {
	// Now returns the current monotonic time.
	Now() time.Time
	// Unix returns the current time as an integer unix second.
	Unix() int64
}

// SystemClock is the production clock backed by the host wall clock. Wall time
// is only ever used through this adapter so tests can substitute a fake.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
func (SystemClock) Unix() int64    { return time.Now().Unix() }

// FakeClock is a deterministic clock that advances only when the test asks it
// to. It is used by every public test that exercises a time-window boundary.
type FakeClock struct {
	t time.Time
}

// NewFakeClock builds a FakeClock pinned at the supplied instant.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{t: t} }

func (f *FakeClock) Now() time.Time { return f.t }
func (f *FakeClock) Unix() int64    { return f.t.Unix() }

// Advance moves the fake clock forward by d.
func (f *FakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

// TimeWindow is a closed [Start, End] construction window expressed as integer
// unix seconds. Tightening timestamps must fall inside it.
type TimeWindow struct {
	Start int64 `json:"start_unix"`
	End   int64 `json:"end_unix"`
}

// ContainsUnix reports whether ts falls within the closed window.
func (w TimeWindow) ContainsUnix(ts int64) bool {
	return ts >= w.Start && ts <= w.End
}

// Contains reports whether t falls within the closed window.
func (w TimeWindow) Contains(t time.Time) bool { return w.ContainsUnix(t.Unix()) }
