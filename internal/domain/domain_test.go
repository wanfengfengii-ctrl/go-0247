package domain_test

import (
	"reflect"
	"testing"

	"boltforge-highstrength-joint-qa/internal/domain"
)

func TestErrorReasonSortingIsDeterministic(t *testing.T) {
	err := domain.NewError(domain.CodePairMismatch, "mismatch").
		WithReasons([]domain.Reason{
			{Code: domain.CodeGradeMismatch, Node: "N-02", Bolt: 2},
			{Code: domain.CodeSocketMismatch, Node: "N-01", Bolt: 1},
			{Code: domain.CodeUnknownPair, Node: "N-01", Bolt: 2},
			{Code: domain.CodeDuplicatePair, Node: "N-02", Bolt: 1},
		})

	got := err.Codes()
	want := []string{"SOCKET_MISMATCH", "UNKNOWN_PAIR", "DUPLICATE_PAIR", "GRADE_MISMATCH"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}

	// Node ordering must come first, then bolt number.
	first := err.SortedReasons()[0]
	if first.Node != "N-01" || first.Bolt != 1 {
		t.Fatalf("first reason = %+v, want N-01/1", first)
	}
	last := err.SortedReasons()[len(err.SortedReasons())-1]
	if last.Node != "N-02" || last.Bolt != 2 {
		t.Fatalf("last reason = %+v, want N-02/2", last)
	}
}

func TestIsCode(t *testing.T) {
	err := domain.NewError(domain.CodeTokenBusy, "busy")
	if !domain.IsCode(err, domain.CodeTokenBusy) {
		t.Fatal("expected IsCode to match TOKEN_BUSY")
	}
	if domain.IsCode(err, domain.CodeDeviceBusy) {
		t.Fatal("expected IsCode to not match DEVICE_BUSY")
	}
}

func TestErrorRevisionCarried(t *testing.T) {
	err := domain.NewError(domain.CodeStaleRevision, "stale").WithRevision(7)
	if err.Revision != 7 {
		t.Fatalf("revision = %d, want 7", err.Revision)
	}
}

func TestRangeContains(t *testing.T) {
	r := domain.Range{Min: 100, Max: 900}
	cases := []struct {
		v    int
		want bool
	}{
		{100, true}, {900, true}, {500, true}, {99, false}, {901, false},
	}
	for _, c := range cases {
		if got := r.Contains(c.v); got != c.want {
			t.Errorf("Range.Contains(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestBoltKey(t *testing.T) {
	if got := domain.BoltKey("N-01", 3); got != "N-01/3" {
		t.Fatalf("BoltKey = %q, want %q", got, "N-01/3")
	}
}
