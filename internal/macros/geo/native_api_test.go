package geo

import (
	"testing"

	"github.com/openpaily/paily-check/internal/interfaces"
)

func ev(code, provider string) NativeGeoEvidence {
	return NativeGeoEvidence{
		Info:     &interfaces.GeoInfo{CountryCode: code},
		Provider: provider,
	}
}

// Two agreeing sources settle the answer without waiting for the rest: that is
// the whole point of the quorum, since one fast source is what produced the
// region churn this replaced.
func TestNativeGeoTallySettlesOnQuorum(t *testing.T) {
	tally := newNativeGeoTally(2)

	if _, ok := tally.record(ev("US", "ipwhois")); ok {
		t.Fatal("one reply must not reach quorum 2")
	}
	if _, ok := tally.record(ev("DE", "myip")); ok {
		t.Fatal("a conflicting second reply must not reach quorum")
	}

	got, ok := tally.record(ev("US", "ip-sb"))
	if !ok {
		t.Fatal("two of three agreeing must settle")
	}
	if got.Info.CountryCode != "US" {
		t.Fatalf("country = %q, want US", got.Info.CountryCode)
	}
	if got.Votes != 2 {
		t.Fatalf("votes = %d, want 2", got.Votes)
	}
	if len(got.AgreedWith) != 2 || got.AgreedWith[0] != "ipwhois" || got.AgreedWith[1] != "ip-sb" {
		t.Fatalf("agreedWith = %v, want [ipwhois ip-sb]", got.AgreedWith)
	}
	if got.Provider != "ipwhois" {
		t.Fatalf("provider = %q, want the first source that named the country", got.Provider)
	}
}

// No quorum still has to produce an answer. Returning nothing would drop the
// node from distribution, which is worse than an uncertain country.
func TestNativeGeoTallyWithoutQuorumKeepsBest(t *testing.T) {
	tally := newNativeGeoTally(3)
	ch := make(chan NativeGeoEvidence, 3)
	for _, e := range []NativeGeoEvidence{ev("US", "a"), ev("DE", "b"), ev("JP", "c")} {
		ch <- e
	}

	got := tally.finish(ch)
	if got.Info == nil {
		t.Fatal("a single reply must still be returned")
	}
	if got.Votes != 1 {
		t.Fatalf("votes = %d, want 1 (it is one source's opinion, not a consensus)", got.Votes)
	}
	if len(got.AgreedWith) != 1 {
		t.Fatalf("agreedWith = %v, want exactly one provider", got.AgreedWith)
	}
}

func TestNativeGeoTallyNoReplies(t *testing.T) {
	tally := newNativeGeoTally(2)
	if got := tally.finish(make(chan NativeGeoEvidence)); got.Info != nil {
		t.Fatalf("no replies must yield empty evidence, got %+v", got.Info)
	}
}

// A quorum above the number of configured providers would never settle, so the
// caller clamps it; the tally itself must also refuse a nonsensical value.
func TestNewNativeGeoTallyClampsQuorum(t *testing.T) {
	if got := newNativeGeoTally(0).quorum; got != 1 {
		t.Fatalf("quorum = %d, want 1", got)
	}
	if got := newNativeGeoTally(-3).quorum; got != 1 {
		t.Fatalf("quorum = %d, want 1", got)
	}
}

// A provider that returns a nil Info must not be counted as a vote.
func TestNativeGeoTallyIgnoresNilInfo(t *testing.T) {
	tally := newNativeGeoTally(1)
	if _, ok := tally.record(NativeGeoEvidence{Provider: "broken"}); ok {
		t.Fatal("nil Info must not settle the answer")
	}
	if tally.bestVotes != 0 {
		t.Fatalf("bestVotes = %d, want 0", tally.bestVotes)
	}
}
