package match

import "testing"

func mustNew(t *testing.T, mode Mode, pat string, fold bool, min float64) Matcher {
	t.Helper()
	m, err := New(mode, pat, fold, min)
	if err != nil {
		t.Fatalf("New(%q): %v", pat, err)
	}
	return m
}

func TestFuzzyAcceptsLooseTokens(t *testing.T) {
	m := mustNew(t, Fuzzy, "instal cli", true, 0.55)
	if _, ok := m.Score("Install the CLI:"); !ok {
		t.Fatal("expected loose match")
	}
	if _, ok := m.Score("uninstall everything"); ok {
		t.Fatal("missing token cli should not match")
	}
}

func TestFuzzyRequiresOrderWithinToken(t *testing.T) {
	m := mustNew(t, Fuzzy, "abc", true, 0.55)
	if _, ok := m.Score("a x b x c"); ok {
		t.Fatal("scattered characters should fall under the threshold")
	}
	if _, ok := m.Score("cba"); ok {
		t.Fatal("reversed characters should not match")
	}
}

func TestExactSubstringScoresHigherThanScattered(t *testing.T) {
	m := mustNew(t, Fuzzy, "deploy", true, 0.55)
	tight, ok := m.Score("run deploy now")
	if !ok {
		t.Fatal("substring should match")
	}
	loose, ok := m.Score("d e p l o y")
	if ok && loose >= tight {
		t.Fatalf("scattered score %v should be below tight %v", loose, tight)
	}
}

func TestThresholdIsHonoured(t *testing.T) {
	strict := mustNew(t, Fuzzy, "canry", true, 0.95)
	if _, ok := strict.Score("canary rollout"); ok {
		t.Fatal("gapped match should be rejected at 0.95")
	}
	loose := mustNew(t, Fuzzy, "canry", true, 0.4)
	if _, ok := loose.Score("canary rollout"); !ok {
		t.Fatal("gapped match should be accepted at 0.4")
	}
}

func TestSmartCase(t *testing.T) {
	if !SmartCase("deploy") {
		t.Fatal("lowercase pattern should fold")
	}
	if SmartCase("Deploy") {
		t.Fatal("pattern with uppercase should not fold")
	}
}

func TestCaseSensitivity(t *testing.T) {
	m := mustNew(t, Fuzzy, "Deploy", false, 0.55)
	if _, ok := m.Score("deploy the thing"); ok {
		t.Fatal("case-sensitive match should reject lowercase")
	}
	if _, ok := m.Score("Deploy the thing"); !ok {
		t.Fatal("case-sensitive match should accept exact case")
	}
}

func TestSubstringSpans(t *testing.T) {
	m := mustNew(t, Substring, "foo", true, 0)
	spans := m.Spans("Foo and foo")
	want := []Span{{0, 3}, {8, 11}}
	if len(spans) != len(want) {
		t.Fatalf("spans = %v, want %v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("spans = %v, want %v", spans, want)
		}
	}
}

func TestFuzzySpansCoverMatchedRunes(t *testing.T) {
	m := mustNew(t, Fuzzy, "brew instal", true, 0.55)
	line := "  - On macOS run `brew install foo`"
	spans := m.Spans(line)
	if len(spans) == 0 {
		t.Fatal("expected highlight spans")
	}
	var covered string
	for _, s := range spans {
		covered += line[s.Start:s.End]
	}
	if covered != "brewinstal" {
		t.Fatalf("covered = %q, want %q", covered, "brewinstal")
	}
}

func TestSpansSkipUnrelatedLines(t *testing.T) {
	m := mustNew(t, Fuzzy, "kubernetes", true, 0.55)
	if spans := m.Spans("nothing to see"); len(spans) != 0 {
		t.Fatalf("spans = %v, want none", spans)
	}
}

func TestMultibyteSpansAreRuneAligned(t *testing.T) {
	m := mustNew(t, Fuzzy, "café", true, 0.55)
	line := "le café noir"
	spans := m.Spans(line)
	if len(spans) == 0 {
		t.Fatal("expected spans")
	}
	if line[spans[0].Start:spans[len(spans)-1].End] != "café" {
		t.Fatalf("span text = %q", line[spans[0].Start:spans[len(spans)-1].End])
	}
}

func TestRegexpMode(t *testing.T) {
	m := mustNew(t, Regexp, `canary [0-9]+`, true, 0)
	if _, ok := m.Score("foo deploy --canary 10"); !ok {
		t.Fatal("regexp should match")
	}
	if _, ok := m.Score("canary rollout"); ok {
		t.Fatal("regexp should not match")
	}
	if _, err := New(Regexp, "(", true, 0); err == nil {
		t.Fatal("invalid regexp should error")
	}
}

func TestAllMatchesEverythingWithoutHighlighting(t *testing.T) {
	m := All()
	for _, s := range []string{"", "anything at all"} {
		if _, ok := m.Score(s); !ok {
			t.Fatalf("All should score %q", s)
		}
	}
	if spans := m.Spans("anything at all"); spans != nil {
		t.Fatalf("spans = %v, want none", spans)
	}
}

func TestMergeSpans(t *testing.T) {
	got := Merge([]Span{{5, 8}, {0, 3}, {3, 6}})
	if len(got) != 1 || got[0] != (Span{0, 8}) {
		t.Fatalf("merged = %v, want [{0 8}]", got)
	}
}
