// Unified diff generation, equivalent to Python's difflib.unified_diff(a, b,
// lineterm="", n=1). Two stages: a line-based Myers diff (O(ND)), then grouping into
// hunks with the same rules as difflib's get_grouped_opcodes. Checked against
// difflib with 300+ fuzz cases (equivalent as patches).
package filewatch

import "strings"

type opTag int

const (
	opEqual opTag = iota
	opDelete
	opInsert
)

type op struct {
	tag    opTag
	i1, i2 int
	j1, j2 int
}

// Handles the main cases of Python's str.splitlines() (\r\n, \r, \n), which is enough for notes and code.
func SplitLines(text string) []string {
	var lines []string
	start := 0
	i := 0
	for i < len(text) {
		c := text[i]
		if c == '\n' {
			lines = append(lines, text[start:i])
			i++
			start = i
		} else if c == '\r' {
			lines = append(lines, text[start:i])
			i++
			if i < len(text) && text[i] == '\n' {
				i++
			}
			start = i
		} else {
			i++
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// Myers O(ND) diff. Finds the shortest edit script from a to b and returns it as
// equal/delete/insert opcodes (runs of the same operation merged into ranges).
func getOpcodes(a, b []string) []op {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}

	var trace [][]int
	offset := max
	v := make([]int, 2*max+1)
	found := false

	for d := 0; d <= max && !found; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		trace = append(trace, snapshot)
		if found {
			break
		}
	}
	if len(trace) == 0 {
		trace = append(trace, append([]int(nil), v...))
	}

	type point struct{ x, y int }
	var points []point
	cx, cy := n, m
	points = append(points, point{cx, cy})
	for d := len(trace) - 1; d >= 0 && (cx > 0 || cy > 0); d-- {
		vv := trace[d]
		k := cx - cy
		var prevK int
		if k == -d || (k != d && vv[offset+k-1] < vv[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vv[offset+prevK]
		prevY := prevX - prevK

		for cx > prevX && cy > prevY {
			points = append(points, point{cx - 1, cy - 1})
			cx--
			cy--
		}
		if d > 0 {
			points = append(points, point{prevX, prevY})
			cx, cy = prevX, prevY
		}
	}
	// reverse
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}

	var ops []op
	for i := 1; i < len(points); i++ {
		x0, y0 := points[i-1].x, points[i-1].y
		x1, y1 := points[i].x, points[i].y
		var tag opTag
		switch {
		case x1 == x0+1 && y1 == y0+1:
			tag = opEqual
		case x1 == x0+1 && y1 == y0:
			tag = opDelete
		case x1 == x0 && y1 == y0+1:
			tag = opInsert
		default:
			continue
		}
		if len(ops) > 0 && ops[len(ops)-1].tag == tag {
			ops[len(ops)-1].i2 = x1
			ops[len(ops)-1].j2 = y1
		} else {
			ops = append(ops, op{tag, x0, x1, y0, y1})
		}
	}
	return ops
}

func formatRangeUnified(start, stop int) string {
	beginning := start + 1
	length := stop - start
	if length == 1 {
		return itoa(beginning)
	}
	if length == 0 {
		beginning--
	}
	return itoa(beginning) + "," + itoa(length)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// Port of difflib.SequenceMatcher.get_grouped_opcodes(n): keeps only n lines around
// each change and splits hunks at long equal runs.
func getGroupedOpcodes(codes []op, n int) [][]op {
	var groups [][]op
	if len(codes) == 0 {
		codes = []op{{opEqual, 0, 1, 0, 1}}
	} else {
		codes = append([]op(nil), codes...)
	}

	if codes[0].tag == opEqual {
		c := codes[0]
		i1 := max(c.i1, c.i2-n)
		j1 := max(c.j1, c.j2-n)
		codes[0] = op{opEqual, i1, c.i2, j1, c.j2}
	}
	if codes[len(codes)-1].tag == opEqual {
		c := codes[len(codes)-1]
		i2 := min(c.i2, c.i1+n)
		j2 := min(c.j2, c.j1+n)
		codes[len(codes)-1] = op{opEqual, c.i1, i2, c.j1, j2}
	}

	nn := n + n
	var group []op
	for _, o := range codes {
		if o.tag == opEqual && o.i2-o.i1 > nn {
			group = append(group, op{opEqual, o.i1, min(o.i2, o.i1+n), o.j1, min(o.j2, o.j1+n)})
			groups = append(groups, group)
			group = nil
			i1 := max(o.i1, o.i2-n)
			j1 := max(o.j1, o.j2-n)
			o = op{opEqual, i1, o.i2, j1, o.j2}
		}
		group = append(group, o)
	}
	if len(group) > 0 && !(len(group) == 1 && group[0].tag == opEqual) {
		groups = append(groups, group)
	}
	return groups
}

// Output format: "--- " and "+++ " first (empty, as no file names are given), then
// each hunk as "@@ -a,b +c,d @@" followed by lines starting with " ", "-" or "+".
func UnifiedDiff(a, b []string, n int) []string {
	opcodes := getOpcodes(a, b)
	groups := getGroupedOpcodes(opcodes, n)
	var result []string
	started := false
	for _, group := range groups {
		if !started {
			started = true
			result = append(result, "--- ", "+++ ")
		}
		first := group[0]
		last := group[len(group)-1]
		range1 := formatRangeUnified(first.i1, last.i2)
		range2 := formatRangeUnified(first.j1, last.j2)
		result = append(result, "@@ -"+range1+" +"+range2+" @@")

		for _, o := range group {
			switch o.tag {
			case opEqual:
				for i := o.i1; i < o.i2; i++ {
					result = append(result, " "+a[i])
				}
			case opDelete:
				for i := o.i1; i < o.i2; i++ {
					result = append(result, "-"+a[i])
				}
			case opInsert:
				for j := o.j1; j < o.j2; j++ {
					result = append(result, "+"+b[j])
				}
			}
		}
	}
	return result
}

func JoinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
