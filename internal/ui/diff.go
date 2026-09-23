package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Unified diff syntax.
const (
	markFrom  = "--- "
	markTo    = "+++ "
	markHunk  = "@@ "
	markGit   = "diff --git "
	pathNew   = "b/"
	devNull   = "/dev/null"
	opContext = ' '
	opDel     = '-'
	opAdd     = '+'

	maxDiffLines  = 40
	tabWidth      = 4
	sgrReset      = "\x1b[0m"
	sgrResetShort = "\x1b[m"
)

// Row backgrounds, 24-bit; the terminal downsamples when needed.
const (
	bgDelDark  = "\x1b[48;2;75;24;24m"
	bgAddDark  = "\x1b[48;2;27;58;31m"
	bgDelLight = "\x1b[48;2;255;224;224m"
	bgAddLight = "\x1b[48;2;220;245;220m"
)

// diffLine is one hunk body line with its display number:
// the new-side number for context/additions, old-side for deletions.
type diffLine struct {
	op   byte
	num  int
	text string
}

// diffHunk is a run of lines plus the file it belongs to (for the lexer).
type diffHunk struct {
	file  string
	lines []diffLine
}

// parsedDiff is unified diff text split into hunks. trailer holds text
// after the last hunk that is not diff syntax, e.g. "[exit status 1]".
type parsedDiff struct {
	hunks   []diffHunk
	files   int
	trailer []string
}

// parseDiff reads unified diff text (git or plain). A hunk takes exactly
// the line counts its header announces; other headers are dropped.
func parseDiff(s string) parsedDiff {
	var (
		d                parsedDiff
		file             string
		old, new         int
		oldLeft, newLeft int
	)

	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		// Inside a hunk every line is body, even one that looks like a header.
		if oldLeft > 0 || newLeft > 0 {
			h := &d.hunks[len(d.hunks)-1]
			op, text := byte(opContext), ""
			if l != "" {
				op, text = l[0], l[1:]
			}

			switch op {
			case opContext:
				h.lines = append(h.lines, diffLine{op: op, num: new, text: text})
				old, new, oldLeft, newLeft = old+1, new+1, oldLeft-1, newLeft-1
			case opDel:
				h.lines = append(h.lines, diffLine{op: op, num: old, text: text})
				old, oldLeft = old+1, oldLeft-1
			case opAdd:
				h.lines = append(h.lines, diffLine{op: op, num: new, text: text})
				new, newLeft = new+1, newLeft-1
			}
			continue
		}

		switch {
		case strings.HasPrefix(l, markTo):
			file = strings.TrimPrefix(strings.TrimPrefix(l, markTo), pathNew)
			d.files++
			d.trailer = nil
		case strings.HasPrefix(l, markHunk):
			old, oldLeft, new, newLeft = hunkRange(l)
			d.hunks = append(d.hunks, diffHunk{file: file})
			d.trailer = nil
		case len(d.hunks) > 0 && !isDiffMeta(l):
			d.trailer = append(d.trailer, l)
		}
	}

	d.trailer = trimBlank(d.trailer)
	return d
}

// isDiffMeta reports git header lines between files, e.g. "index ...".
func isDiffMeta(l string) bool {
	for _, p := range []string{markGit, markFrom, "index ", "new file", "deleted file",
		"similarity", "rename ", "old mode", "new mode", "\\ No newline"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// hunkRange parses "@@ -12,5 +12,6 @@" into (12, 5, 12, 6).
// A missing count means 1, as in "@@ -3 +3 @@".
func hunkRange(l string) (oldStart, oldCount, newStart, newCount int) {
	fields := strings.Fields(l)
	if len(fields) < 3 {
		return 1, 0, 1, 0
	}
	oldStart, oldCount = rangeOf(fields[1])
	newStart, newCount = rangeOf(fields[2])
	return
}

func rangeOf(f string) (start, count int) {
	a, b, hasCount := strings.Cut(f[1:], ",")
	start, _ = strconv.Atoi(a)
	if !hasCount {
		return start, 1
	}
	count, _ = strconv.Atoi(b)
	return start, count
}

// diff renders unified diff text: file headers (multi-file only), line
// numbers, full-width red/green rows and syntax-highlighted code.
//
//	12   func main() {
//	13 - 	println("old")
//	13 + 	println("new")
func (r *renderer) diff(s string) string {
	d := parseDiff(s)
	hunks, files := d.hunks, d.files
	if len(hunks) == 0 {
		return paintLines(r.pal.dim, head(s))
	}

	numWidth := len(strconv.Itoa(maxLineNum(hunks)))
	var rows []string
	for i, h := range hunks {
		newFile := i == 0 || h.file != hunks[i-1].file
		if files > 1 && newFile && h.file != devNull {
			rows = append(rows, r.pal.tool.Render(h.file))
		} else if !newFile {
			rows = append(rows, r.pal.dim.Render(strings.Repeat(" ", numWidth)+" ⋯"))
		}
		rows = append(rows, r.hunk(h, numWidth)...)
	}

	hidden := max(len(rows)-maxDiffLines, 0)
	rows = rows[:len(rows)-hidden]
	out := strings.Join(rows, "\n")
	if hidden > 0 {
		out += "\n" + r.moreNote(hidden)
	}
	if len(d.trailer) > 0 {
		out += "\n" + paintLines(r.pal.dim, strings.Join(d.trailer, "\n"))
	}
	return out
}

// hunk highlights both sides of a hunk as whole snippets, so multi-line
// constructs lex correctly, then zips them back into rows.
func (r *renderer) hunk(h diffHunk, numWidth int) []string {
	var oldSide, newSide []string
	for _, l := range h.lines {
		text := strings.ReplaceAll(l.text, "\t", strings.Repeat(" ", tabWidth))
		if l.op != opAdd {
			oldSide = append(oldSide, text)
		}
		if l.op != opDel {
			newSide = append(newSide, text)
		}
	}
	oldHL := r.highlightLines(oldSide, h.file)
	newHL := r.highlightLines(newSide, h.file)

	rows := make([]string, 0, len(h.lines))
	var oi, ni int
	for _, l := range h.lines {
		var code string
		switch l.op {
		case opDel:
			code, oi = oldHL[oi], oi+1
		case opAdd:
			code, ni = newHL[ni], ni+1
		default:
			code, oi, ni = newHL[ni], oi+1, ni+1
		}
		rows = append(rows, r.diffRow(l, code, numWidth))
	}
	return rows
}

func (r *renderer) highlightLines(lines []string, file string) []string {
	if len(lines) == 0 {
		return nil
	}

	out := strings.Split(r.hl.code(strings.Join(lines, "\n"), file, ""), "\n")
	// A lexer may merge or drop trailing empty lines; fall back to plain.
	if len(out) != len(lines) {
		return lines
	}
	return out
}

// diffRow lays out "<num> <op> <code>" and paints the background across
// the full width. Chroma resets are re-armed with the row background.
func (r *renderer) diffRow(l diffLine, code string, numWidth int) string {
	num := fmt.Sprintf("%*d", numWidth, l.num)
	width := max(r.width-len(gutter), numWidth+4)

	if l.op == opContext {
		return ansi.Truncate(r.pal.dim.Render(num)+"   "+code, width, "…")
	}

	bg, mark := r.rowBg(l.op), r.pal.add.Render("+")
	if l.op == opDel {
		mark = r.pal.del.Render("-")
	}

	row := ansi.Truncate(num+" "+mark+" "+code, width, "…")
	row = strings.NewReplacer(sgrReset, sgrReset+bg, sgrResetShort, sgrResetShort+bg).Replace(row)
	pad := strings.Repeat(" ", max(width-ansi.StringWidth(row), 0))
	return bg + row + pad + sgrReset
}

func (r *renderer) rowBg(op byte) string {
	switch {
	case op == opDel && r.dark:
		return bgDelDark
	case op == opDel:
		return bgDelLight
	case r.dark:
		return bgAddDark
	}
	return bgAddLight
}

func maxLineNum(hunks []diffHunk) int {
	n := 0
	for _, h := range hunks {
		for _, l := range h.lines {
			n = max(n, l.num)
		}
	}
	return n
}

// looksLikeDiff spots unified diff output, e.g. from git diff.
func looksLikeDiff(s string) bool {
	return strings.HasPrefix(s, markGit) ||
		strings.HasPrefix(s, markFrom) && strings.Contains(s, "\n"+markTo) && strings.Contains(s, "\n"+markHunk)
}
