package topics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// TestTokenizerReplay compares tokenizer v2 with the frozen legacy tokenizer
// over a JSONL file of {"text": "..."} posts named by HS_TOKENIZER_REPLAY_JSONL.
// It prints and asserts nothing: it exists so a tokenizer change is read
// against real posts before it ships.
func TestTokenizerReplay(t *testing.T) {
	path := os.Getenv("HS_TOKENIZER_REPLAY_JSONL")
	if path == "" {
		t.Skip("HS_TOKENIZER_REPLAY_JSONL not set")
	}
	all := readReplayTexts(t, path)
	// Ingest tokenizes only the posts Eligible accepts, so the replay does too.
	var texts []string
	for _, text := range all {
		if Eligible(text) {
			texts = append(texts, text)
		}
	}
	t.Logf("posts: %d, eligible for tokenizing: %d", len(all), len(texts))

	type run struct {
		name     string
		tokenize func(string) []string
	}
	runs := []run{{"legacy", tokenizeLegacy}, {"v2", Tokenize}}
	scores := make([][]TermScore, len(runs))
	byDF := make([][]TermScore, len(runs))
	bigramDF := make([][]TermScore, len(runs))
	dfs := make([]map[string]int, len(runs))
	for ri, r := range runs {
		rows := make([]store.TopicTokenRow, 0, len(texts))
		df := map[string]int{}
		var bigrams, funcSide, yearBigrams int
		funcDistinct := map[string]bool{}
		for i, text := range texts {
			tokens := r.tokenize(text)
			for _, tok := range tokens {
				a, b, ok := strings.Cut(tok, "_")
				if !ok {
					continue
				}
				bigrams++
				if functionWords[a] || functionWords[b] {
					funcSide++
					funcDistinct[tok] = true
				}
				if yearPattern.MatchString(a) || yearPattern.MatchString(b) {
					yearBigrams++
				}
			}
			seen := map[string]bool{}
			for _, tok := range tokens {
				if !seen[tok] {
					seen[tok] = true
					df[tok]++
				}
			}
			js, err := json.Marshal(tokens)
			if err != nil {
				t.Fatalf("marshal tokens: %v", err)
			}
			rows = append(rows, store.TopicTokenRow{
				PostURI:   fmt.Sprintf("at://did:plc:x%d/app.bsky.feed.post/%d", i+1, i+1),
				Tokens:    string(js),
				AuthorDID: fmt.Sprintf("did:plc:x%d", i+1),
			})
		}
		scores[ri] = ComputeTFIDF(rows)
		byDF[ri] = rankByDocFreq(df, false)
		bigramDF[ri] = rankByDocFreq(df, true)
		dfs[ri] = df
		t.Logf("%s: bigram occurrences %d, with a function-word side %d (%d distinct), year bigrams %d, scored terms %d",
			r.name, bigrams, funcSide, len(funcDistinct), yearBigrams, len(scores[ri]))
	}

	var crossBoundary, legacyBigrams int
	for _, text := range texts {
		pairs := segmentPairs(text)
		for _, tok := range tokenizeLegacy(text) {
			if !strings.Contains(tok, "_") {
				continue
			}
			legacyBigrams++
			if !pairs[tok] {
				crossBoundary++
			}
		}
	}
	t.Logf("legacy: %d of %d bigram occurrences cross a v2 segment boundary", crossBoundary, legacyBigrams)

	sideBySide(t, fmt.Sprintf("top 50 by TF-IDF (MinDocFrequency=%d)", MinDocFrequency), scores)
	sideBySide(t, "top 50 by document frequency (df >= 2)", byDF)
	sideBySide(t, "top 50 bigrams by document frequency (df >= 2)", bigramDF)

	var v2Only []TermScore
	for term, n := range dfs[1] {
		if strings.Contains(term, "_") && dfs[0][term] == 0 {
			v2Only = append(v2Only, TermScore{Term: term, DocFreq: n})
		}
	}
	sort.Slice(v2Only, func(i, j int) bool {
		if v2Only[i].DocFreq != v2Only[j].DocFreq {
			return v2Only[i].DocFreq > v2Only[j].DocFreq
		}
		return v2Only[i].Term < v2Only[j].Term
	})
	t.Logf("top 20 of %d v2-only bigrams (absent from legacy) by document frequency", len(v2Only))
	for i, ts := range v2Only {
		if i == 20 {
			break
		}
		t.Logf("%-4d %s (%d)", i+1, ts.Term, ts.DocFreq)
	}

	for ri, r := range runs {
		for _, in := range []struct {
			name  string
			terms []TermScore
		}{{"TF-IDF terms", scores[ri]}, {"df >= 2 bigrams", bigramDF[ri]}} {
			phrases := detectOverlappingPhrases(in.terms)
			t.Logf("%s: %d overlapping phrases from %s", r.name, len(phrases), in.name)
			for _, p := range phrases {
				t.Logf("%s phrase: %q from %v", r.name, p.Phrase, p.Terms)
			}
		}
	}
}

func sideBySide(t *testing.T, title string, lists [][]TermScore) {
	t.Helper()
	t.Logf("%s", title)
	t.Logf("%-4s %-36s %-36s", "rank", "legacy", "v2")
	for i := 0; i < 50; i++ {
		if i >= len(lists[0]) && i >= len(lists[1]) {
			break
		}
		t.Logf("%-4d %-36s %-36s", i+1, replayTerm(lists[0], i), replayTerm(lists[1], i))
	}
}

// rankByDocFreq lists terms seen in at least two posts, most frequent first,
// as TermScores with Score = DocFreq, optionally bigrams only.
func rankByDocFreq(df map[string]int, bigramsOnly bool) []TermScore {
	var out []TermScore
	for term, n := range df {
		if n < 2 || (bigramsOnly && !strings.Contains(term, "_")) {
			continue
		}
		out = append(out, TermScore{Term: term, Score: float64(n), DocFreq: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DocFreq != out[j].DocFreq {
			return out[i].DocFreq > out[j].DocFreq
		}
		return out[i].Term < out[j].Term
	})
	return out
}

// segmentPairs returns every adjacent word pair v2 segmentation allows,
// ignoring word classes and dropping every digit word as the legacy tokenizer
// did. A legacy bigram outside this set spans a sentence, URL, mention or
// emoji boundary.
func segmentPairs(text string) map[string]bool {
	pairs := map[string]bool{}
	for _, seg := range segmentWords(text) {
		var prev string
		for _, w := range seg {
			if pureDigits.MatchString(w) {
				continue
			}
			if prev != "" {
				pairs[prev+"_"+w] = true
			}
			prev = w
		}
	}
	return pairs
}

func replayTerm(s []TermScore, i int) string {
	if i >= len(s) {
		return ""
	}
	return fmt.Sprintf("%s (%d)", s[i].Term, s[i].DocFreq)
}

func readReplayTexts(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open replay file: %v", err)
	}
	defer f.Close()
	var texts []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var rec struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		texts = append(texts, rec.Text)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read replay file: %v", err)
	}
	return texts
}

// tokenizeLegacy is a frozen copy of Tokenize as it stood before tokenizer v2
// (2026-11-09): punctuation-blind bigrams kept when either side is not a
// stopword, and every pure-digit word dropped.
func tokenizeLegacy(text string) []string {
	text = urlPattern.ReplaceAllString(text, "")
	text = bareDomainPat.ReplaceAllString(text, "")
	text = mentionPattern.ReplaceAllString(text, "")
	text = stripEmojiLegacy(text)
	text = strings.ToLower(text)
	text = nonAlphaNum.ReplaceAllString(text, " ")

	// Bigrams must be built before stopword filtering so that compound
	// terms like "halftime_show" survive even when one part is a stopword.
	var candidates []string
	for _, word := range strings.Fields(text) {
		if len(word) < 3 {
			continue
		}
		if pureDigits.MatchString(word) {
			continue
		}
		candidates = append(candidates, word)
	}

	var bigrams []string
	for i := 0; i < len(candidates)-1; i++ {
		a, b := candidates[i], candidates[i+1]
		if !legacyStopwords[a] || !legacyStopwords[b] {
			bigrams = append(bigrams, a+"_"+b)
		}
	}

	var unigrams []string
	for _, word := range candidates {
		if !legacyStopwords[word] {
			unigrams = append(unigrams, word)
		}
	}

	tokens := make([]string, 0, len(unigrams)+len(bigrams))
	tokens = append(tokens, unigrams...)
	tokens = append(tokens, bigrams...)
	return tokens
}

func stripEmojiLegacy(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isEmoji(r) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var legacyStopwords = map[string]bool{
	// pronouns & determiners
	"the": true, "this": true, "that": true, "these": true, "those": true,
	"you": true, "your": true, "yours": true, "yourself": true,
	"him": true, "his": true, "her": true, "hers": true, "herself": true, "himself": true,
	"she": true, "they": true, "them": true, "their": true, "theirs": true, "themselves": true,
	"its": true, "itself": true, "our": true, "ours": true, "ourselves": true,
	"who": true, "whom": true, "whose": true, "which": true, "what": true,
	"one": true, "ones": true, "someone": true, "anyone": true, "everyone": true,
	"something": true, "anything": true, "everything": true, "nothing": true, "nobody": true,
	"myself": true, "mine": true,

	// conjunctions & prepositions
	"and": true, "but": true, "for": true, "nor": true, "yet": true,
	"with": true, "from": true, "into": true, "than": true, "about": true,
	"over": true, "between": true, "through": true, "before": true, "after": true,
	"because": true, "although": true, "though": true, "unless": true, "whether": true,
	"without": true, "within": true, "during": true, "toward": true, "towards": true,
	"against": true, "among": true, "upon": true, "along": true, "across": true, "around": true,
	"under": true, "behind": true, "above": true, "below": true, "beside": true, "beyond": true,

	// common verbs / auxiliaries
	"are": true, "was": true, "were": true, "been": true, "being": true,
	"not": true, "all": true, "any": true, "could": true, "would": true, "should": true,
	"had": true, "has": true, "have": true, "will": true, "may": true,
	"did": true, "does": true, "let": true, "say": true, "said": true,
	"get": true, "got": true, "see": true, "saw": true, "use": true, "used": true,
	"make": true, "made": true, "take": true, "took": true, "give": true, "gave": true,
	"come": true, "came": true, "keep": true, "kept": true, "put": true,
	"know": true, "knew": true, "think": true, "thought": true,
	"want": true, "need": true, "look": true, "find": true, "found": true,
	"tell": true, "told": true, "feel": true, "felt": true, "become": true, "left": true,
	"try": true, "tried": true, "turn": true, "run": true, "move": true, "call": true,
	"start": true, "started": true, "help": true,

	// adverbs & adjectives
	"too": true, "very": true, "also": true, "just": true, "even": true,
	"only": true, "more": true, "most": true, "much": true, "many": true,
	"some": true, "such": true, "each": true, "every": true, "other": true,
	"new": true, "now": true, "old": true, "long": true, "still": true,
	"well": true, "same": true, "down": true, "own": true, "real": true,
	"right": true, "good": true, "best": true, "big": true, "little": true, "last": true,
	"how": true, "when": true, "where": true, "then": true, "here": true, "there": true,
	"never": true, "always": true, "already": true,
	"why": true, "while": true, "again": true, "next": true, "hard": true, "easy": true,
	"high": true, "low": true, "full": true, "whole": true, "able": true, "least": true,
	"less": true, "close": true, "open": true, "early": true, "late": true,
	"free": true, "true": true, "wrong": true, "different": true, "probably": true,
	"maybe": true, "perhaps": true, "almost": true, "quite": true, "pretty": true,
	"especially": true, "basically": true, "honestly": true, "clearly": true,
	"apparently": true, "definitely": true, "absolutely": true, "completely": true,
	"entire": true, "exactly": true, "simply": true, "seriously": true,

	// common filler nouns
	"way": true, "out": true, "back": true, "time": true, "year": true, "years": true,
	"day": true, "days": true, "night": true, "morning": true, "evening": true,
	"week": true, "weeks": true, "month": true, "months": true, "hour": true, "hours": true,
	"first": true, "second": true, "third": true, "thing": true, "things": true,
	"two": true, "three": true, "four": true, "five": true, "six": true,
	"seven": true, "eight": true, "nine": true, "ten": true, "hundred": true, "thousand": true, "million": true,
	"people": true, "person": true, "folks": true, "kids": true, "children": true,
	"man": true, "men": true, "woman": true, "women": true, "guy": true, "girl": true,
	"life": true, "world": true, "part": true, "country": true, "state": true,
	"point": true, "home": true, "place": true, "lot": true, "end": true, "head": true,
	"hand": true, "side": true, "fact": true, "case": true, "number": true,
	"money": true, "power": true, "human": true, "reason": true, "question": true,
	"answer": true, "problem": true, "idea": true, "story": true, "word": true, "words": true,
	"name": true, "line": true, "type": true, "form": true,
	"details": true, "via": true, "news": true, "information": true,
	"group": true, "level": true, "area": true, "system": true, "order": true,
	"result": true, "moment": true, "course": true, "example": true, "view": true,
	"half": true, "kind": true, "sort": true, "sense": true, "mind": true,
	"job": true, "jobs": true, "stuff": true, "bit": true, "piece": true,
	"set": true, "video": true, "videos": true, "music": true, "photo": true, "photos": true,
	"support": true, "another": true, "game": true, "team": true, "season": true, "episode": true,

	// generic descriptors (race/color/gender — too common as general language to be topical)
	"black": true, "white": true, "color": true, "colors": true,
	"male": true, "female": true, "gender": true,

	// temporal words
	"jan": true, "feb": true, "mar": true, "apr": true, "jun": true,
	"jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true,
	"january": true, "february": true, "march": true, "april": true,
	"june": true, "july": true, "august": true, "september": true,
	"october": true, "november": true, "december": true,
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
	"weekend": true,

	// contraction fragments (apostrophe stripped by nonAlphaNum)
	"don": true, "didn": true, "doesn": true, "isn": true, "wasn": true,
	"aren": true, "weren": true, "won": true, "wouldn": true, "couldn": true,
	"shouldn": true, "hasn": true, "hadn": true, "haven": true, "can": true,
	"ain": true, "gonna": true, "gotta": true, "wanna": true,

	// social media filler
	"lol": true, "lmao": true, "omg": true, "wtf": true, "tbh": true,
	"imo": true, "smh": true, "idk": true, "ngl": true, "bruh": true,
	"literally": true, "actually": true, "really": true,
	"like": true, "going": true, "yeah": true, "yes": true, "nah": true,
	"sure": true, "okay": true, "thank": true, "thanks": true,
	"please": true, "sorry": true, "great": true,

	// generic sentiment / reaction words (always present, never topical)
	"love": true, "hate": true, "happy": true, "sad": true, "hope": true,
	"fuck": true, "shit": true, "fucking": true, "damn": true, "ass": true,
	"hell": true, "crap": true, "suck": true, "sucks": true, "stupid": true,
	"bad": true, "worse": true, "worst": true, "better": true,
	"amazing": true, "awesome": true, "beautiful": true, "funny": true,
	"crazy": true, "cool": true, "nice": true, "weird": true,

	// meta-discussion words (describe discourse, not subjects)
	"opinion": true, "opinions": true, "debate": true, "reaction": true, "reactions": true,
	"controversy": true, "controversial": true, "criticism": true, "critique": true,
	"thoughts": true, "perspective": true, "perspectives": true, "takes": true,
	"discourse": true, "argument": true, "arguments": true, "discussion": true,
	"response": true, "responses": true, "statement": true, "statements": true,
	"comment": true, "comments": true, "views": true, "stance": true,

	// generic activity verbs & gerunds
	"watch": true, "watching": true, "show": true, "play": true, "playing": true,
	"read": true, "post": true, "posted": true, "share": true, "follow": true,
	"work": true, "working": true, "stop": true, "wait": true, "talk": true, "talking": true,
	"remember": true, "believe": true, "understand": true, "happen": true, "happened": true,
	"change": true, "live": true, "win": true, "lost": true, "lose": true,
	"doing": true, "getting": true, "having": true, "making": true, "trying": true,
	"looking": true, "saying": true, "coming": true,
	"taking": true, "giving": true, "telling": true, "thinking": true, "feeling": true,
	"leaving": true, "running": true, "putting": true, "keeping": true, "calling": true,
	"asking": true, "using": true, "seeing": true, "hearing": true, "starting": true,
	"wanting": true, "needing": true, "caring": true, "meaning": true,

	// URL / web noise
	"www": true, "http": true, "https": true, "url": true,
	"com": true, "org": true, "net": true, "edu": true, "gov": true,
	"html": true, "php": true,
	"website": true, "link": true, "click": true, "subscribe": true,

	// platform names (always present, never topical)
	"youtube": true, "youtu": true, "twitter": true, "tiktok": true, "instagram": true,
	"facebook": true, "reddit": true, "bluesky": true, "bsky": true,
	"feed": true, "app": true, "bot": true, "thread": true,

	// commerce / spam (fake jersey bots, product listings)
	"jersey": true, "jerseys": true, "stitched": true, "untouchable": true, "vapor": true,
	"shipping": true, "discount": true, "coupon": true, "promo": true, "price": true,
	"sale": true, "checkout": true, "cart": true,
	"buy": true, "sell": true, "selling": true, "buying": true, "shop": true, "store": true,
	"limited": true, "edition": true, "authentic": true, "replica": true, "wholesale": true,

	// misc high-frequency filler
	"dont": true, "doesnt": true, "cant": true, "wont": true, "didnt": true,
	"isnt": true, "wasnt": true, "arent": true, "werent": true, "havent": true,
	"off": true, "finally": true, "tonight": true, "today": true, "tomorrow": true,
	"yesterday": true, "ago": true, "since": true, "until": true,
	"ever": true, "away": true, "enough": true, "else": true,
	"fun": true, "friends": true, "friend": true, "care": true, "needs": true,
	"might": true, "check": true, "face": true, "art": true, "media": true,
	"house": true, "goes": true, "gone": true, "random": true, "randomly": true,
}
