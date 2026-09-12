package topics

// blockedTerms are words that must never reach a published topic label,
// description, or alt text. The grouping model writes that text from tokens
// harvested off the firehose, so a coordinated push of slurs into the term
// list is the obvious way to make this account post one.
//
// This list is a floor, not a ceiling: it holds only unambiguous slurs and
// abuse terms, matched per whole word after lowercasing, and it is not a
// content-moderation system. The real defences are the label allowlist regex
// in grouper.go (which rejects anything outside a narrow character set) and
// the generic-label filter. Add to it when something gets through; do not
// rely on it alone.
var blockedTerms = []string{
	"nigger",
	"nigga",
	"faggot",
	"fag",
	"tranny",
	"shemale",
	"kike",
	"spic",
	"beaner",
	"wetback",
	"chink",
	"gook",
	"paki",
	"raghead",
	"coon",
	"dyke",
	"retard",
	"retarded",
	"groomer",
	"kys",
}

// blockedTermSet indexes blockedTerms for per-word lookup.
var blockedTermSet = func() map[string]bool {
	m := make(map[string]bool, len(blockedTerms))
	for _, t := range blockedTerms {
		m[t] = true
	}
	return m
}()
