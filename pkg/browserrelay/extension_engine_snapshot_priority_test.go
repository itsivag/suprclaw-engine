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

