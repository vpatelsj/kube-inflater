package naming

import "testing"

func TestHollowNodeName(t *testing.T) {
	if got, want := HollowNodeName(7), "hollow-node-7"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNextAvailableNodeNumber(t *testing.T) {
	cases := []struct {
		in   []string
		want int
	}{
		{nil, 1},
		{[]string{"hollow-node-1"}, 2},
		{[]string{"hollow-node-2", "hollow-node-10", "foo"}, 11},
	}
	for _, c := range cases {
		if got := NextAvailableNodeNumber(c.in); got != c.want {
			t.Fatalf("got %d want %d for %v", got, c.want, c.in)
		}
	}
}
