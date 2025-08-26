package perf

import "testing"

func TestSummarize(t *testing.T) {
	min, max, avg := Summarize([]float64{1, 2, 3, 4})
	if min != 1 || max != 4 || avg != 2.5 {
		t.Fatalf("got min=%v max=%v avg=%v", min, max, avg)
	}
	min, max, avg = Summarize(nil)
	if min != 0 || max != 0 || avg != 0 {
		t.Fatalf("empty should return zeros")
	}
}
