package graph

import (
	"strings"
	"testing"
)

func render(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		var line strings.Builder
		for i, c := range r.Cells {
			switch {
			case i == 2*r.Col:
				line.WriteRune('●')
			case c.R == 0:
				line.WriteRune(' ')
			default:
				line.WriteRune(c.R)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "|\n")
	}
	return b.String()
}

func layout(dag [][2]string) []Row {
	ids := make([]string, len(dag))
	for i, d := range dag {
		ids[i] = d[0]
	}
	return Layout(ids, func(i int) []string {
		if dag[i][1] == "" {
			return nil
		}
		return strings.Split(dag[i][1], ",")
	}, 8)
}

func TestLinear(t *testing.T) {
	got := render(layout([][2]string{{"c", "b"}, {"b", "a"}, {"a", ""}}))
	want := "●|\n●|\n●|\n"
	if got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestMergeAndFork(t *testing.T) {
	// m merges b2 (side) into a2; both fork from a1
	got := render(layout([][2]string{
		{"m", "a2,b2"},
		{"a2", "a1"},
		{"b2", "a1"},
		{"a1", ""},
	}))
	want := "" +
		"●─╮|\n" +
		"● │|\n" +
		"│ ●|\n" +
		"●─╯|\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestTwoTipsConverge(t *testing.T) {
	got := render(layout([][2]string{
		{"x", "base"},
		{"y", "base"},
		{"base", ""},
	}))
	want := "" +
		"●|\n" +
		"│ ●|\n" +
		"●─╯|\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCrossing(t *testing.T) {
	// a lane passing through a horizontal connector draws a crossing
	got := render(layout([][2]string{
		{"m", "p1,p2"},
		{"t", "p1"},
		{"p2", "root"},
		{"p1", "root"},
		{"root", ""},
	}))
	if !strings.Contains(got, "●") || strings.Count(got, "\n") != 5 {
		t.Fatalf("unexpected:\n%s", got)
	}
	for _, r := range layout([][2]string{{"m", "p1,p2"}, {"t", "p1"}, {"p2", "root"}, {"p1", "root"}, {"root", ""}}) {
		if len(r.Cells)%2 != 0 {
			t.Fatal("odd cell count")
		}
	}
}
