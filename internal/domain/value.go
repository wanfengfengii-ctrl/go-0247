package domain

// Revision is a monotonically increasing directory or aggregate revision.
// Stale revisions are rejected before any write is applied.
type Revision int64

// Generation identifies the construction generation of a task. A task's
// generation is fixed at lock time and any other generation is rejected.
type Generation int64

// OperationNo is the client-supplied idempotency key carried on every
// mutating request.
type OperationNo string

// TorqueNm is an integer newton-metre reading.
type TorqueNm int

// AngleDeg is an integer degree reading.
type AngleDeg int

// Range is a closed inclusive [Min, Max] bound over an integer quantity. It is
// used for the allowed torque and angle intervals fixed at lock time.
type Range struct {
	Min int
	Max int
}

// Contains reports whether v falls within the closed interval.
func (r Range) Contains(v int) bool {
	return v >= r.Min && v <= r.Max
}
