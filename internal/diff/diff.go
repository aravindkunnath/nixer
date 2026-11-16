// Package diff renders small unified diffs for showing config edits.
package diff

import (
	"fmt"
	"strings"
)

type op struct {
	kind  byte // ' ', '-' or '+'
	text  string
	oldNo int // 1-based line numbers of the next old/new line at this op
	newNo int
}

// Unified returns a unified diff of a and b with ctx lines of context, or ""
// if they are equal.
func Unified(name, a, b string, ctx int) string {
	if a == b {
		return ""
	}
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	ops := script(x, y)

	var changes []int
	for i, o := range ops {
		if o.kind != ' ' {
			changes = append(changes, i)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", name, name)
	for i := 0; i < len(changes); {
		start, end := changes[i], changes[i]
		for i++; i < len(changes) && changes[i]-end <= 2*ctx; i++ {
			end = changes[i]
		}
		lo, hi := max(start-ctx, 0), min(end+ctx+1, len(ops))
		var oldN, newN int
		for _, o := range ops[lo:hi] {
			if o.kind != '+' {
				oldN++
			}
			if o.kind != '-' {
				newN++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", ops[lo].oldNo, oldN, ops[lo].newNo, newN)
		for _, o := range ops[lo:hi] {
			sb.WriteString(string(o.kind) + o.text + "\n")
		}
	}
	return sb.String()
}

// script builds an edit script, trimming the common prefix and suffix so the
// LCS table only covers the changed middle.
func script(x, y []string) []op {
	p := 0
	for p < len(x) && p < len(y) && x[p] == y[p] {
		p++
	}
	s := 0
	for s < len(x)-p && s < len(y)-p && x[len(x)-1-s] == y[len(y)-1-s] {
		s++
	}
	mx, my := x[p:len(x)-s], y[p:len(y)-s]

	var mid []op
	n, m := len(mx), len(my)
	t := make([][]int, n+1)
	for i := range t {
		t[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if mx[i] == my[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && mx[i] == my[j]:
			mid = append(mid, op{kind: ' ', text: mx[i]})
			i++
			j++
		case i < n && (j == m || t[i+1][j] >= t[i][j+1]):
			mid = append(mid, op{kind: '-', text: mx[i]})
			i++
		default:
			mid = append(mid, op{kind: '+', text: my[j]})
			j++
		}
	}

	var ops []op
	for _, l := range x[:p] {
		ops = append(ops, op{kind: ' ', text: l})
	}
	ops = append(ops, mid...)
	for _, l := range x[len(x)-s:] {
		ops = append(ops, op{kind: ' ', text: l})
	}
	oldNo, newNo := 1, 1
	for i := range ops {
		ops[i].oldNo, ops[i].newNo = oldNo, newNo
		if ops[i].kind != '+' {
			oldNo++
		}
		if ops[i].kind != '-' {
			newNo++
		}
	}
	return ops
}
