package repo

import "testing"

func TestMerge3(t *testing.T) {
	base := "a\nb\nc\nd\ne\n"
	cases := []struct {
		name, a, b, want string
		ok               bool
	}{
		{"disjoint", "A\nb\nc\nd\ne\n", "a\nb\nc\nd\nE\n", "A\nb\nc\nd\nE\n", true},
		{"same change", "a\nB\nc\nd\ne\n", "a\nB\nc\nd\ne\n", "a\nB\nc\nd\ne\n", true},
		{"insert both ends", "x\na\nb\nc\nd\ne\n", "a\nb\nc\nd\ne\ny\n", "x\na\nb\nc\nd\ne\ny\n", true},
		{"delete vs edit elsewhere", "a\nc\nd\ne\n", "a\nb\nc\nd\nE\n", "a\nc\nd\nE\n", true},
		{"conflict", "a\nX\nc\nd\ne\n", "a\nY\nc\nd\ne\n", "a\n<<<<<<< ours\nX\n=======\nY\n>>>>>>> theirs\nc\nd\ne\n", false},
	}
	for _, c := range cases {
		got, ok := Merge3(base, c.a, c.b, "ours", "theirs")
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got ok=%v\n%q\nwant ok=%v\n%q", c.name, ok, got, c.ok, c.want)
		}
	}
}
