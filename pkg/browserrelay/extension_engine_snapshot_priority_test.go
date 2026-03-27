package browserrelay

import "testing"

func TestPrioritizeSnapshotElementsPrefersAddToCart(t *testing.T) {
	in := []snapshotElement{
		{Selector: "#without-exchange", Role: "button", Text: "Without Exchange", Tag: "button"},
		{Selector: "#add-to-cart", Role: "button", Text: "Add to Cart", Tag: "button"},
		{Selector: "#buy-now", Role: "button", Text: "Buy Now", Tag: "button"},
	}

	got := prioritizeSnapshotElements(in, 1)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Selector != "#add-to-cart" {
		t.Fatalf("top selector = %q, want #add-to-cart", got[0].Selector)
	}
}

func TestPrioritizeSnapshotElementsPreservesDeterministicTieOrder(t *testing.T) {
	in := []snapshotElement{
		{Selector: "#first", Role: "link", Text: "Open", Tag: "a"},
		{Selector: "#second", Role: "link", Text: "Open", Tag: "a"},
	}

	got := prioritizeSnapshotElements(in, 2)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Selector != "#first" || got[1].Selector != "#second" {
		t.Fatalf("unexpected order: %#v", got)
	}
}

func TestPrioritizeSnapshotElementsKeepsCoverageBeyondTopScores(t *testing.T) {
	in := []snapshotElement{
		{Selector: "#a1", Role: "button", Text: "Add to Cart", Tag: "button"},
		{Selector: "#a2", Role: "button", Text: "Buy Now", Tag: "button"},
		{Selector: "#a3", Role: "button", Text: "Checkout", Tag: "button"},
		{Selector: "#r1", Role: "link", Text: "iPhone 17 256GB", Tag: "a"},
		{Selector: "#r2", Role: "link", Text: "iPhone 17 Pro Max", Tag: "a"},
	}

	got := prioritizeSnapshotElements(in, 4)
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	seen := map[string]struct{}{}
	for _, el := range got {
		seen[el.Selector] = struct{}{}
	}
	if _, ok := seen["#r1"]; !ok {
		t.Fatalf("expected coverage element #r1 to remain in prioritized set: %#v", got)
	}
}
