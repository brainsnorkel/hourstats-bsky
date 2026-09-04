package stats

import "testing"

func TestCollectorLanguages(t *testing.T) {
	c := &Collector{}
	if got := c.LanguagesSinceAnalysis(); len(got) != 0 {
		t.Fatalf("fresh = %v", got)
	}
	for i := 0; i < 3; i++ {
		c.IncrementLanguage("en")
	}
	c.IncrementLanguage("pt")
	got := c.LanguagesSinceAnalysis()
	if got["en"] != 3 || got["pt"] != 1 || len(got) != 2 {
		t.Errorf("counts = %v", got)
	}
	if again := c.LanguagesSinceAnalysis(); len(again) != 0 {
		t.Errorf("not reset: %v", again)
	}
	// Junk tags beyond the cap fold into "other".
	for i := 0; i < maxLanguageKeys+10; i++ {
		c.IncrementLanguage(string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)))
	}
	got = c.LanguagesSinceAnalysis()
	if len(got) != maxLanguageKeys+1 || got["other"] != 10 {
		t.Errorf("cap: keys=%d other=%d", len(got), got["other"])
	}
}
