package semver

import "testing"

func TestCompareOrdersDottedVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"0.4.2", "0.5.0", -1},
		{"0.5.0", "0.4.2", 1},
		{"0.5.0", "0.5.0", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"v0.2.0", "0.2.0", 0},
		{"1", "1.0.0", 0},
		{"1.2", "1.2.0", 0},
		{"1.2.1", "1.2", 1},
	}
	for _, tc := range cases {
		got, err := Compare(tc.a, tc.b)
		if err != nil {
			t.Errorf("Compare(%q, %q): %v", tc.a, tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareRejectsUnparseableVersions(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "   ", "next", "1.2.3.4", "1.x", "-1.0.0", "1.2.3-rc1"} {
		if _, err := Compare(value, "1.0.0"); err == nil {
			t.Errorf("Compare(%q, 1.0.0) accepted an unparseable version", value)
		}
		if Valid(value) {
			t.Errorf("Valid(%q) = true", value)
		}
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()
	newer, err := IsNewer("0.6.0", "0.5.0")
	if err != nil || !newer {
		t.Fatalf("IsNewer(0.6.0, 0.5.0) = %v, %v", newer, err)
	}
	newer, err = IsNewer("0.5.0", "0.5.0")
	if err != nil || newer {
		t.Fatalf("IsNewer(0.5.0, 0.5.0) = %v, %v", newer, err)
	}
	if _, err := IsNewer("0.6.0", "not-a-version"); err == nil {
		t.Fatal("IsNewer accepted an unparseable installed version")
	}
}
