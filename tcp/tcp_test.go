package tcp

import "testing"

func TestIsBetweenWrapped(t *testing.T) {
	tests := []struct {
		start, x, end uint32
		expected      bool
	}{
		{0, 500, 1000, true},
		{1000, 1500, 100, true},
		{1000, 200, 500, true},
		{1000, 500, 0, false},
		{1000, 100, 1500, false},
	}

	for _, test := range tests {
		result := isBetweenWrapped(test.start, test.x, test.end)
		if result != test.expected {
			t.Errorf("isBetweenWrapped(%d, %d, %d) = %v; want %v", test.start, test.x, test.end, result, test.expected)
		}
	}
}
