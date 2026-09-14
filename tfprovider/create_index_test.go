package tfprovider

import "testing"

// Terraform creates siblings in arbitrary order; the requested index at creation must be
// one OBS can honour now, so creation never fails on order alone.
func TestClampCreateIndex(t *testing.T) {
	cases := []struct{ declared, max, want int64 }{
		{0, 0, 0},  // first item into an empty container
		{5, 0, 0},  // declared deep, nothing there yet
		{5, 2, 2},  // only two siblings exist: append
		{2, 5, 2},  // fits: keep the declared index
		{-1, 5, 0}, // never negative
		{3, -1, 0}, // an empty container reported as -1 (len-1) still yields 0
	}
	for _, c := range cases {
		if got := clampCreateIndex(c.declared, c.max); got != c.want {
			t.Errorf("clampCreateIndex(%d, %d)=%d want %d", c.declared, c.max, got, c.want)
		}
	}
}
