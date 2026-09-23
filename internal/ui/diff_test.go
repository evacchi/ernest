package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const sample = `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -10,4 +10,5 @@ func main() {
 	a := 1
-	b := 2
+	b := 3
+	c := 4
 	/* multi
 	   line */
@@ -40,2 +41,2 @@
-x
+y
`

func TestParseDiff(t *testing.T) {
	d := parseDiff(sample)
	hunks, files := d.hunks, d.files
	if files != 1 || len(hunks) != 2 || hunks[0].file != "main.go" {
		t.Fatalf("files=%d hunks=%+v", files, hunks)
	}

	// context 10, del 11 (old), add 11, add 12, context 13, 14
	want := []struct {
		op  byte
		num int
	}{{' ', 10}, {'-', 11}, {'+', 11}, {'+', 12}, {' ', 13}, {' ', 14}}
	for i, w := range want {
		l := hunks[0].lines[i]
		if l.op != w.op || l.num != w.num {
			t.Errorf("line %d = %c%d, want %c%d", i, l.op, l.num, w.op, w.num)
		}
	}

	if l := hunks[1].lines[0]; l.op != '-' || l.num != 40 {
		t.Errorf("hunk 2 first = %c%d", l.op, l.num)
	}
	if l := hunks[1].lines[1]; l.op != '+' || l.num != 41 {
		t.Errorf("hunk 2 second = %c%d", l.op, l.num)
	}
}

func TestRenderDiff(t *testing.T) {
	r := newRenderer(true)
	r.setWidth(60)
	out := r.diff(sample)

	if os.Getenv("SHOW") != "" {
		t.Log("\n" + out)
	}

	rows := strings.Split(out, "\n")
	plain := make([]string, len(rows))
	for i, row := range rows {
		plain[i] = ansi.Strip(row)
	}

	wantPrefix := []string{"10   ", "11 - ", "11 + ", "12 + ", "13   ", "14   ", "   ⋯", "40 - ", "41 + "}
	if len(plain) != len(wantPrefix) {
		t.Fatalf("rows = %q", plain)
	}
	for i, p := range wantPrefix {
		if !strings.HasPrefix(plain[i], p) {
			t.Errorf("row %d = %q, want prefix %q", i, plain[i], p)
		}
	}

	// Changed rows span the full width and tabs are expanded.
	for _, i := range []int{1, 2, 3, 7, 8} {
		if w := ansi.StringWidth(rows[i]); w != r.width-len(gutter) {
			t.Errorf("row %d width = %d", i, w)
		}
	}
	// Every reset inside a changed row re-arms the background, or the
	// color stops mid-row.
	for _, i := range []int{1, 2, 3, 7, 8} {
		body := strings.TrimSuffix(rows[i], sgrReset)
		bg := r.rowBg(plain[i][len("11 ")])
		for _, reset := range []string{sgrReset, sgrResetShort} {
			if n := strings.Count(body, reset); n != strings.Count(body, reset+bg) {
				t.Errorf("row %d: %q not followed by background", i, reset)
			}
		}
	}

	if strings.Contains(out, "\t") {
		t.Error("tab not expanded")
	}
}

func TestLooksLikeDiff(t *testing.T) {
	if !looksLikeDiff(sample) || !looksLikeDiff("--- a/x\n+++ b/x\n@@ -1 +1 @@\n") {
		t.Error("missed diff")
	}
	if looksLikeDiff("--- just a rule\ntext") {
		t.Error("false positive")
	}
}

// Lines past the hunk counts are not diff lines, e.g. bash's exit status.
func TestDiffTrailer(t *testing.T) {
	const out = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n x := 1\n-y := 2\n+y := 3\n\n[exit status 1]"

	hunks := parseDiff(out).hunks
	if len(hunks) != 1 || len(hunks[0].lines) != 3 {
		t.Fatalf("hunks = %+v", hunks)
	}

	r := newRenderer(true)
	plain := ansi.Strip(r.diff(out))
	if !strings.HasSuffix(plain, "[exit status 1]") {
		t.Errorf("trailer missing:\n%s", plain)
	}
	if strings.Contains(plain, "\n3") {
		t.Errorf("phantom context line:\n%s", plain)
	}
}
