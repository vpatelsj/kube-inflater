package plan

import "testing"

func TestCalculateBatchSize(t *testing.T) {
	cases := []struct{ init, factor, batch, want int }{
		{5, 2, 1, 5}, {5, 2, 2, 10}, {5, 2, 3, 20},
		{1, 3, 4, 27}, {10, 1, 3, 10},
	}
	for _, c := range cases {
		if got := CalculateBatchSize(c.init, c.factor, c.batch); got != c.want {
			t.Fatalf("CalculateBatchSize(%d,%d,%d)=%d, want %d", c.init, c.factor, c.batch, got, c.want)
		}
	}
}

func TestCalculateBatchesPlan(t *testing.T) {
	p := CalculateBatchesPlan(5, 2, 37, 10)
	want := [][2]int{{1, 5}, {2, 10}, {3, 20}, {4, 2}}
	if len(p) != len(want) {
		t.Fatalf("plan len=%d, want %d", len(p), len(want))
	}
	for i := range want {
		if p[i] != want[i] {
			t.Fatalf("plan[%d]=%v, want %v", i, p[i], want[i])
		}
	}
}

func TestCalculateBatchesPlan500k(t *testing.T) {
	target := 500000
	p := CalculateBatchesPlan(100, 2, target, 30)

	total := 0
	for _, batch := range p {
		total += batch[1]
	}
	if total != target {
		t.Fatalf("total=%d, want %d (batches=%d)", total, target, len(p))
	}

	// First batch should be 100
	if p[0][1] != 100 {
		t.Fatalf("first batch size=%d, want 100", p[0][1])
	}

	// Should fit within 30 batches
	if len(p) > 30 {
		t.Fatalf("batch count=%d, exceeds max 30", len(p))
	}
}
