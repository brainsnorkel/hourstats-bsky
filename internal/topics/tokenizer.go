package topics

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	urlPattern     = regexp.MustCompile(`https?://\S+`)
	bareDomainPat  = regexp.MustCompile(`(?i)\b[\w][\w.-]*\.(com|org|net|edu|gov|io|co|dev|app|me|us|uk|info|xyz|be|tv|gg|ly)\b`)
	mentionPattern = regexp.MustCompile(`@[\w.-]+`)
	nonAlphaNum    = regexp.MustCompile(`[^a-z0-9]+`)
	pureDigits     = regexp.MustCompile(`^\d+$`)
	yearPattern    = regexp.MustCompile(`^(19|20)\d\d$`)
	// segmentBreak matches sentence punctuation. "!", "?", ";", newlines,
	// "|", the Unicode ellipsis, bullet and CJK/fullwidth stops always break;
	// a run of two or more dots always breaks; an em dash breaks only with
	// whitespace on both sides; a single "." or ":" breaks only before
	// whitespace or the end of the text, so "26.1", "8:00" and "e.g" stay
	// whole. splitSegments then keeps a lone "." after an abbreviation.
	segmentBreak = regexp.MustCompile(`[!?;\n|\x{2026}\x{3002}\x{FF01}\x{FF1F}\x{2022}]+|\s+\x{2014}\s+|\.{2,}|[.:]+(?:\s|$)`)
	// contraction joins "n't" to its word ("won't" -> "wont"), before
	// nonAlphaNum would split it into "won" and "t".
	contraction = regexp.MustCompile(`n['\x{2019}]t\b`)
)

// periodAbbreviations are the words a "." follows without ending a sentence:
// "Lakers vs. Celtics", "Sen. Warren". A single letter before the "." (the
// last letter of "U.S." or "e.g.", or an initial) does not end one either.
// "no" is not here: "No. 1" is an abbreviation only before a digit, while
// "says no. The" ends a sentence (abbreviationBefore).
var periodAbbreviations = map[string]bool{
	"vs": true, "sen": true, "rep": true, "gov": true, "gen": true, "col": true,
	"sgt": true, "mr": true, "mrs": true, "ms": true, "dr": true, "st": true,
	"jr": true, "inc": true,
}

// segmentBoundary replaces every removed URL, mention and emoji, so the words
// on either side of one land in different segments and never form a bigram.
const segmentBoundary = "\n"

// wordClass decides whether a word may be a unigram and whether it may be one
// side of a bigram.
type wordClass int

const (
	// classPlain: a unigram, and a bigram side with any non-function word.
	classPlain wordClass = iota
	// classFunction (functionWords): never a unigram, never a bigram side.
	classFunction
	// classContent (contentStopwords): never a unigram; a bigram side only
	// next to a plain word, so white_house and halftime_show survive.
	classContent
	// classYear (^(19|20)\d\d$): never a unigram; a bigram side only next
	// to a plain word, so 2026_midterms reaches the grouper.
	classYear
	// classDigits: any other pure-digit word, dropped entirely.
	classDigits
)

func classify(word string) wordClass {
	switch {
	case functionWords[word]:
		return classFunction
	case contentStopwords[word]:
		return classContent
	case word == "" || word[0] < '0' || word[0] > '9':
		return classPlain
	case yearPattern.MatchString(word):
		return classYear
	case pureDigits.MatchString(word):
		return classDigits
	}
	return classPlain
}

// Tokenize returns a post's unigrams followed by its bigrams. Bigrams are
// built only between adjacent words of one segment (see segmentWords). A pair
// is kept when it is one of the stopwordCompounds, or when neither side is a
// function word and at least one side is a plain word. Pure-digit words other
// than years are dropped before pairing, and "the" is skipped over like the
// one- and two-letter words already are, so "State of the Union" still yields
// state_union.
func Tokenize(text string) []string {
	var unigrams, bigrams []string
	for _, seg := range segmentWords(text) {
		prev, prevClass := "", classFunction
		for _, word := range seg {
			class := classify(word)
			if class == classDigits || word == "the" {
				continue
			}
			if class == classPlain {
				unigrams = append(unigrams, word)
			}
			if prev != "" {
				pair := prev + "_" + word
				if stopwordCompounds[pair] ||
					(class != classFunction && prevClass != classFunction &&
						(class == classPlain || prevClass == classPlain)) {
					bigrams = append(bigrams, pair)
				}
			}
			prev, prevClass = word, class
		}
	}

	tokens := make([]string, 0, len(unigrams)+len(bigrams))
	tokens = append(tokens, unigrams...)
	tokens = append(tokens, bigrams...)
	return tokens
}

// segmentWords lowercases the text and splits it into segments at sentence
// punctuation (see segmentBreak and splitSegments) and at every URL, bare
// domain, mention and emoji, returning each segment's words of three or more
// characters. URLs and mentions are removed before lowercasing and splitting,
// since they contain dots and colons themselves. "n't" is joined to its word
// first, so "won't" is "wont" rather than "won".
func segmentWords(text string) [][]string {
	text = urlPattern.ReplaceAllString(text, segmentBoundary)
	text = bareDomainPat.ReplaceAllString(text, segmentBoundary)
	text = mentionPattern.ReplaceAllString(text, segmentBoundary)
	text = stripEmoji(text)
	text = strings.ToLower(text)
	text = contraction.ReplaceAllString(text, "nt")

	var segments [][]string
	for _, seg := range splitSegments(text) {
		var words []string
		for _, word := range strings.Fields(nonAlphaNum.ReplaceAllString(seg, " ")) {
			if len(word) >= 3 {
				words = append(words, word)
			}
		}
		if len(words) > 0 {
			segments = append(segments, words)
		}
	}
	return segments
}

// splitSegments splits lowercased text at every segmentBreak match, except a
// lone "." after a periodAbbreviations word, a single letter, or a "no"
// followed by a digit.
func splitSegments(text string) []string {
	var segs []string
	start := 0
	for _, m := range segmentBreak.FindAllStringIndex(text, -1) {
		if strings.TrimSpace(text[m[0]:m[1]]) == "." && abbreviationBefore(text[:m[0]], text[m[1]:]) {
			continue
		}
		segs = append(segs, text[start:m[0]])
		start = m[1]
	}
	return append(segs, text[start:])
}

// abbreviationBefore reports whether the text ends in a word whose following
// "." does not end a sentence: a periodAbbreviations word, a single letter,
// or "no" when the text after the break starts with a digit ("No. 1").
func abbreviationBefore(text, after string) bool {
	i := len(text)
	for i > 0 && text[i-1] >= 'a' && text[i-1] <= 'z' {
		i--
	}
	word := text[i:]
	if word == "no" {
		return after != "" && after[0] >= '0' && after[0] <= '9'
	}
	return len(word) == 1 || periodAbbreviations[word]
}

func stripEmoji(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isEmoji(r) {
			b.WriteString(segmentBoundary)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isEmoji(r rune) bool {
	return (r >= 0x1F600 && r <= 0x1F64F) || // emoticons
		(r >= 0x1F300 && r <= 0x1F5FF) || // misc symbols & pictographs
		(r >= 0x1F680 && r <= 0x1F6FF) || // transport & map
		(r >= 0x1F1E0 && r <= 0x1F1FF) || // flags
		(r >= 0x2600 && r <= 0x26FF) || // misc symbols
		(r >= 0x2700 && r <= 0x27BF) || // dingbats
		(r >= 0xFE00 && r <= 0xFE0F) || // variation selectors
		(r >= 0x1F900 && r <= 0x1F9FF) || // supplemental symbols
		(r >= 0x1FA00 && r <= 0x1FA6F) || // chess symbols
		(r >= 0x1FA70 && r <= 0x1FAFF) || // symbols extended-A
		(r >= 0x200D && r <= 0x200D) || // zero width joiner
		(r >= 0x20E3 && r <= 0x20E3) || // combining enclosing keycap
		(r >= 0xE0020 && r <= 0xE007F) // tags
}

// minRepetitionWords is the minimum word count before the repetition
// check kicks in. Very short posts naturally have low diversity.
const minRepetitionWords = 8

// maxRepetitionRatio is the unique-word / total-word threshold below
// which a post is considered keyword-stuffed. Normal prose scores 0.5+;
// spam like "TRUMP EPSTEIN TRUMP EPSTEIN…" scores < 0.1.
const maxRepetitionRatio = 0.3

// maxWordRepetitions is the maximum number of times any single word
// may appear before the post is considered keyword-stuffed. This catches
// posts with a normal preamble followed by a spam tail, which the ratio
// check alone misses because the preamble dilutes the unique/total ratio.
const maxWordRepetitions = 4

// IsRepetitive returns true if the text appears to be keyword-stuffed
// (the same few words repeated many times to game trending topics).
// It checks two conditions (either triggers a true result):
//  1. Low unique/total word ratio (catches pure repetition)
//  2. Any single word appearing more than maxWordRepetitions times
//     (catches "normal preamble + spam tail" patterns)
func IsRepetitive(text string) bool {
	text = strings.ToLower(text)
	words := strings.Fields(text)
	if len(words) < minRepetitionWords {
		return false
	}

	freq := make(map[string]int, len(words))
	for _, w := range words {
		w = stripWordPunctuation(w)
		if w == "" {
			continue
		}
		freq[w]++
	}

	total := 0
	for _, c := range freq {
		total += c
	}
	if total < minRepetitionWords {
		return false
	}

	// Check 1: overall unique/total ratio.
	ratio := float64(len(freq)) / float64(total)
	if ratio < maxRepetitionRatio {
		return true
	}

	// Check 2: any single word repeated excessively.
	for _, count := range freq {
		if count > maxWordRepetitions {
			return true
		}
	}

	return false
}

// Eligible reports whether a post's text is tokenized for trending topics at
// ingest: at most one hashtag and not keyword-stuffed.
func Eligible(text string) bool {
	return strings.Count(text, "#") <= 1 && !IsRepetitive(text)
}

// stripWordPunctuation removes leading/trailing punctuation from a word,
// keeping internal characters like hyphens and apostrophes intact.
func stripWordPunctuation(word string) string {
	return strings.TrimFunc(word, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '\''
	})
}

// functionWords never carry a topic and never name one in combination:
// determiners, pronouns, conjunctions, prepositions, auxiliaries, contraction
// fragments, adverbial filler and interjections. They are neither unigrams
// nor either side of a bigram, so and_vote, the_cybertruck, monitoring_the
// and banger_that are never built.
var functionWords = map[string]bool{
	// pronouns & determiners
	"the": true, "this": true, "that": true, "these": true, "those": true,
	"you": true, "your": true, "yours": true, "yourself": true,
	"him": true, "his": true, "her": true, "hers": true, "herself": true, "himself": true,
	"she": true, "they": true, "them": true, "their": true, "theirs": true, "themselves": true,
	"its": true, "itself": true, "our": true, "ours": true, "ourselves": true,
	"who": true, "whom": true, "whose": true, "which": true, "what": true,
	"ones": true, "someone": true, "anyone": true, "everyone": true,
	"something": true, "anything": true, "everything": true, "nothing": true, "nobody": true,
	"myself": true, "mine": true,

	// quantifiers
	"all": true, "any": true, "some": true, "such": true, "each": true, "every": true,
	"other": true, "another": true, "more": true, "most": true, "much": true, "many": true,
	"least": true, "less": true, "enough": true,

	// conjunctions & prepositions
	"and": true, "but": true, "for": true, "nor": true, "yet": true,
	"with": true, "from": true, "into": true, "than": true, "about": true,
	"over": true, "between": true, "through": true, "before": true, "after": true,
	"because": true, "although": true, "though": true, "unless": true, "whether": true,
	"without": true, "within": true, "during": true, "toward": true, "towards": true,
	"against": true, "among": true, "upon": true, "along": true, "across": true, "around": true,
	"under": true, "behind": true, "above": true, "below": true, "beside": true, "beyond": true,
	"via": true, "since": true, "until": true, "ago": true,
	"today": true, "tonight": true, "tomorrow": true, "yesterday": true,
	"out": true, "off": true, "away": true,

	// auxiliaries
	"are": true, "was": true, "were": true, "been": true, "being": true, "not": true,
	"could": true, "would": true, "should": true, "might": true, "can": true,
	"had": true, "has": true, "have": true, "will": true, "may": true,
	"did": true, "does": true, "having": true, "getting": true, "doing": true, "making": true,
	"going": true, "gonna": true, "gotta": true, "wanna": true,

	// contractions: "n't" is joined to its word before splitting ("won't"
	// -> "wont"); the fragments cover other apostrophes and typed spaces.
	// "won" and "don" are content stopwords (mamdani_won, don_lemon).
	"didn": true, "doesn": true, "isn": true, "wasn": true,
	"aren": true, "weren": true, "wouldn": true, "couldn": true,
	"shouldn": true, "hasn": true, "hadn": true, "haven": true, "ain": true,
	"dont": true, "doesnt": true, "cant": true, "wont": true, "didnt": true,
	"isnt": true, "wasnt": true, "arent": true, "werent": true, "havent": true,
	"couldnt": true, "wouldnt": true, "shouldnt": true, "hasnt": true, "hadnt": true,
	"aint": true, "mustnt": true,

	// adverbial filler
	"too": true, "very": true, "also": true, "just": true, "even": true,
	"only": true, "now": true, "still": true, "well": true,
	"how": true, "when": true, "where": true, "then": true, "here": true, "there": true,
	"never": true, "always": true, "already": true, "ever": true, "else": true,
	"why": true, "while": true, "again": true, "finally": true, "randomly": true,
	"probably": true, "maybe": true, "perhaps": true, "almost": true, "quite": true,
	"especially": true, "basically": true, "honestly": true, "clearly": true,
	"apparently": true, "definitely": true, "absolutely": true, "completely": true,
	"exactly": true, "simply": true, "seriously": true,
	"literally": true, "actually": true, "really": true,

	// interjections & discourse markers
	"lol": true, "lmao": true, "omg": true, "wtf": true, "tbh": true,
	"imo": true, "smh": true, "idk": true, "ngl": true, "bruh": true,
	"like": true, "yeah": true, "yes": true, "nah": true,
	"sure": true, "okay": true, "thank": true, "thanks": true,
	"please": true, "sorry": true,
}

// stopwordCompounds are names the class rules would reject: both halves
// stopwords of either class (white_house, game_over, new_jersey), or one half
// a function word (will_smith, theresa_may). Tokenize consults it before any
// class rule, so it is the one escape hatch for such names; it still applies
// only within one segment.
var stopwordCompounds = map[string]bool{
	"white_house":  true,
	"black_friday": true,
	"new_year":     true,
	"will_smith":   true,
	"theresa_may":  true,
	"game_over":    true,
	"new_jersey":   true,
	"head_start":   true,
	"big_ten":      true,
	"one_piece":    true,
	"good_friday":  true,
}

// contentStopwords are content words too common to be a topic on their own
// but part of many names: never a unigram, and a bigram side only next to a
// plain word (white_house, new_york, world_cup, fox_news, first_amendment,
// real_madrid, election_day, halftime_show, game_thrones, call_duty,
// state_union, gov_newsom, post_banger).
var contentStopwords = map[string]bool{
	"one": true,
	"won": true, "don": true,

	// common verbs
	"let": true, "say": true, "said": true,
	"get": true, "got": true, "see": true, "saw": true, "use": true, "used": true,
	"make": true, "made": true, "take": true, "took": true, "give": true, "gave": true,
	"come": true, "came": true, "keep": true, "kept": true, "put": true,
	"know": true, "knew": true, "think": true, "thought": true,
	"want": true, "need": true, "look": true, "find": true, "found": true,
	"tell": true, "told": true, "feel": true, "felt": true, "become": true, "left": true,
	"try": true, "tried": true, "turn": true, "run": true, "move": true, "call": true,
	"start": true, "started": true, "help": true,

	// adjectives & adverbs with a content sense
	"new": true, "old": true, "long": true, "own": true, "real": true, "same": true,
	"right": true, "good": true, "best": true, "big": true, "little": true, "last": true,
	"down": true, "next": true, "hard": true, "easy": true, "pretty": true,
	"high": true, "low": true, "full": true, "whole": true, "able": true,
	"close": true, "open": true, "early": true, "late": true,
	"free": true, "true": true, "wrong": true, "different": true, "entire": true,

	// common filler nouns
	"way": true, "back": true, "time": true, "year": true, "years": true,
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
	"details": true, "news": true, "information": true,
	"group": true, "level": true, "area": true, "system": true, "order": true,
	"result": true, "moment": true, "course": true, "example": true, "view": true,
	"half": true, "kind": true, "sort": true, "sense": true, "mind": true,
	"job": true, "jobs": true, "stuff": true, "bit": true, "piece": true,
	"set": true, "video": true, "videos": true, "music": true, "photo": true, "photos": true,
	"support": true, "game": true, "team": true, "season": true, "episode": true,

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

	// generic sentiment / reaction words (always present, never topical)
	"love": true, "hate": true, "happy": true, "sad": true, "hope": true, "great": true,
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
	"trying": true, "looking": true, "saying": true, "coming": true,
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
	"fun": true, "friends": true, "friend": true, "care": true, "needs": true,
	"check": true, "face": true, "art": true, "media": true,
	"house": true, "goes": true, "gone": true, "random": true,
}
