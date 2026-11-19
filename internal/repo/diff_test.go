package repo

import (
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	lines, adds, dels := Unified("1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nfive\n6\n7\n8\n9\n", 2)
	if adds != 1 || dels != 1 {
		t.Fatalf("adds=%d dels=%d", adds, dels)
	}
	var b strings.Builder
	for _, l := range lines {
		prefix := map[LineKind]string{LineContext: " ", LineAdd: "+", LineDel: "-", LineHunk: ""}[l.Kind]
		b.WriteString(prefix + l.Text + "\n")
	}
	want := "@@ -3,5 +3,5 @@\n 3\n 4\n-5\n+five\n 6\n 7\n"
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}
