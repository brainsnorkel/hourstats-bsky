package topics

import (
	"regexp"
	"strings"
)

var (
	urlPattern     = regexp.MustCompile(`https?://\S+`)
	bareDomainPat  = regexp.MustCompile(`(?i)\b[\w][\w.-]*\.(com|org|net|edu|gov|io|co|dev|app|me|us|uk|info|xyz|be|tv|gg|ly)\b`)
	mentionPattern = regexp.MustCompile(`@[\w.-]+`)
	nonAlphaNum    = regexp.MustCompile(`[^a-z0-9]+`)
	pureDigits     = regexp.MustCompile(`^\d+$`)
)

func Tokenize(text string) []string {
	text = urlPattern.ReplaceAllString(text, "")
	text = bareDomainPat.ReplaceAllString(text, "")
	text = mentionPattern.ReplaceAllString(text, "")
	text = stripEmoji(text)
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
		if !stopwords[a] || !stopwords[b] {
			bigrams = append(bigrams, a+"_"+b)
		}
	}

	var unigrams []string
	for _, word := range candidates {
		if !stopwords[word] {
			unigrams = append(unigrams, word)
		}
	}

	tokens := make([]string, 0, len(unigrams)+len(bigrams))
	tokens = append(tokens, unigrams...)
	tokens = append(tokens, bigrams...)
	return tokens
}

func stripEmoji(s string) string {
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

var stopwords = map[string]bool{
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

	// generic activity verbs
	"watch": true, "watching": true, "show": true, "play": true, "playing": true,
	"read": true, "post": true, "posted": true, "share": true, "follow": true,
	"work": true, "working": true, "stop": true, "wait": true, "talk": true, "talking": true,
	"remember": true, "believe": true, "understand": true, "happen": true, "happened": true,
	"change": true, "live": true, "win": true, "lost": true, "lose": true,

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
}
