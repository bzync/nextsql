package fulltext

// Snowball English (Porter2) stemmer, revision 1.
//
// The algorithm is the published Snowball English stemmer: deterministic,
// allocation-light, and identical on every replica. Tokens that are not
// lowercase ASCII letters (plus optional apostrophes) pass through unchanged
// so non-English and mixed tokens stay stable.

var englishReplace = map[string]string{
	"skis": "ski", "skies": "sky",
	"dying": "die", "lying": "lie", "tying": "tie",
	"idly": "idl", "gently": "gentl", "ugly": "ugli",
	"early": "earli", "only": "onli", "singly": "singl",
}

var englishInvariant = map[string]struct{}{
	"sky": {}, "news": {}, "howe": {},
	"atlas": {}, "cosmos": {}, "bias": {}, "andes": {},
}

var englishStopAfter1a = map[string]struct{}{
	"inning": {}, "outing": {}, "canning": {},
	"herring": {}, "earring": {},
	"proceed": {}, "exceed": {}, "succeed": {},
}

func stemEnglish(term string) string {
	if !englishStemmable(term) {
		return term
	}
	if _, ok := englishInvariant[term]; ok {
		return term
	}
	if s, ok := englishReplace[term]; ok {
		return s
	}
	buf := make([]byte, len(term)+1) // +1 for the rare step-1b extra 'e'
	copy(buf, term)
	n := prelude(buf[:len(term)])
	if n == 0 {
		return term
	}
	r1, r2 := englishRegions(buf[:n])
	n, r1, r2 = step0(buf, n, r1, r2)
	n, r1, r2 = step1a(buf, n, r1, r2)
	if _, ok := englishStopAfter1a[string(buf[:n])]; ok {
		return postlude(buf[:n])
	}
	n, r1, r2 = step1b(buf, n, r1, r2)
	r1, r2 = englishRegions(buf[:n])
	n, r1, r2 = step1c(buf, n, r1, r2)
	r1, r2 = englishRegions(buf[:n])
	n, r1, r2 = step2(buf, n, r1, r2)
	r1, r2 = englishRegions(buf[:n])
	n, r1, r2 = step3(buf, n, r1, r2)
	r1, r2 = englishRegions(buf[:n])
	n, r1, r2 = step4(buf, n, r1, r2)
	r1, r2 = englishRegions(buf[:n])
	n, _, _ = step5(buf, n, r1, r2)
	if n == 0 {
		return term
	}
	return postlude(buf[:n])
}

func englishStemmable(s string) bool {
	if s == "" {
		return false
	}
	ok := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			continue
		}
		if c < 'a' || c > 'z' {
			return false
		}
		ok = true
	}
	return ok
}

func isVowel(c byte) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'Y':
		return true
	}
	return false
}

func prelude(b []byte) int {
	n := len(b)
	i := 0
	for i < n && b[i] == '\'' {
		i++
	}
	if i > 0 {
		n = copy(b, b[i:])
	}
	if n == 0 {
		return 0
	}
	if b[0] == 'y' {
		b[0] = 'Y'
	}
	for i := 1; i < n; i++ {
		if b[i] == 'y' && isVowel(b[i-1]) {
			b[i] = 'Y'
		}
	}
	return n
}

func postlude(b []byte) string {
	for i, c := range b {
		if c == 'Y' {
			b[i] = 'y'
		}
	}
	return string(b)
}

func englishRegions(b []byte) (r1, r2 int) {
	n := len(b)
	r1, r2 = n, n
	switch {
	case hasPrefix(b, "gener"), hasPrefix(b, "arsen"):
		r1 = 5
	case hasPrefix(b, "commun"):
		r1 = 6
	default:
		r1 = regionAfter(b, 0)
	}
	r2 = regionAfter(b, r1)
	return r1, r2
}

func hasPrefix(b []byte, p string) bool {
	if len(b) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if b[i] != p[i] {
			return false
		}
	}
	return true
}

func regionAfter(b []byte, start int) int {
	n := len(b)
	i := start
	for i < n && !isVowel(b[i]) {
		i++
	}
	for i < n && isVowel(b[i]) {
		i++
	}
	if i < n {
		return i + 1
	}
	return n
}

func ends(b []byte, n int, suf string) bool {
	k := len(suf)
	if n < k {
		return false
	}
	for i := 0; i < k; i++ {
		if b[n-k+i] != suf[i] {
			return false
		}
	}
	return true
}

func inR(off, n int, suf string) bool {
	return n-len(suf) >= off
}

func clip(n, r1, r2, k int) (int, int, int) {
	// R1/R2 stay as original offsets; a later step recomputes them. Shrinking
	// them here would make a replacement suffix look like it sits in R2.
	return n - k, r1, r2
}

func setSuf(b []byte, n, r1, r2 int, suf string) (int, int, int) {
	copy(b[n:], suf)
	n += len(suf)
	return n, r1, r2
}

func replaceSuf(b []byte, n, r1, r2 int, old, neu string) (int, int, int) {
	n, r1, r2 = clip(n, r1, r2, len(old))
	if neu == "" {
		return n, r1, r2
	}
	copy(b[n:], neu)
	return n + len(neu), r1, r2
}

func step0(b []byte, n, r1, r2 int) (int, int, int) {
	switch {
	case ends(b, n, "'s'"):
		return clip(n, r1, r2, 3)
	case ends(b, n, "'s"):
		return clip(n, r1, r2, 2)
	case ends(b, n, "'"):
		return clip(n, r1, r2, 1)
	}
	return n, r1, r2
}

func step1a(b []byte, n, r1, r2 int) (int, int, int) {
	switch {
	case ends(b, n, "sses"):
		return clip(n, r1, r2, 2)
	case ends(b, n, "ied"), ends(b, n, "ies"):
		if n-3 > 1 {
			return clip(n, r1, r2, 2)
		}
		return clip(n, r1, r2, 1)
	case ends(b, n, "us"), ends(b, n, "ss"):
		return n, r1, r2
	case n > 2 && b[n-1] == 's' && hasVowel(b[:n-2]):
		return clip(n, r1, r2, 1)
	}
	return n, r1, r2
}

func hasVowel(b []byte) bool {
	for _, c := range b {
		if isVowel(c) || c == 'y' {
			return true
		}
	}
	return false
}

func step1b(b []byte, n, r1, r2 int) (int, int, int) {
	switch {
	case ends(b, n, "eedly"):
		if inR(r1, n, "eedly") {
			return replaceSuf(b, n, r1, r2, "eedly", "ee")
		}
		return n, r1, r2
	case ends(b, n, "eed"):
		if inR(r1, n, "eed") {
			return replaceSuf(b, n, r1, r2, "eed", "ee")
		}
		return n, r1, r2
	}
	var suf string
	switch {
	case ends(b, n, "edly"):
		suf = "edly"
	case ends(b, n, "ingly"):
		suf = "ingly"
	case ends(b, n, "ed"):
		suf = "ed"
	case ends(b, n, "ing"):
		suf = "ing"
	default:
		return n, r1, r2
	}
	if !hasVowel(b[:n-len(suf)]) {
		return n, r1, r2
	}
	n, r1, r2 = clip(n, r1, r2, len(suf))
	switch {
	case ends(b, n, "at"), ends(b, n, "bl"), ends(b, n, "iz"):
		return setSuf(b, n, r1, r2, "e")
	case doubled(b, n):
		return clip(n, r1, r2, 1)
	case isShort(b, n, r1):
		return setSuf(b, n, r1, r2, "e")
	}
	return n, r1, r2
}

func doubled(b []byte, n int) bool {
	if n < 2 {
		return false
	}
	if b[n-1] != b[n-2] {
		return false
	}
	switch b[n-1] {
	case 'b', 'd', 'f', 'g', 'm', 'n', 'p', 'r', 't':
		return true
	}
	return false
}

func isShort(b []byte, n, r1 int) bool {
	return r1 >= n && shortSyllable(b, n)
}

func shortSyllable(b []byte, n int) bool {
	if n == 2 && isVowel(b[0]) && !isVowel(b[1]) {
		return true
	}
	if n < 3 {
		return false
	}
	c, v, last := b[n-3], b[n-2], b[n-1]
	if isVowel(c) || !isVowel(v) || isVowel(last) {
		return false
	}
	switch last {
	case 'w', 'x', 'Y':
		return false
	}
	return true
}

func step1c(b []byte, n, r1, r2 int) (int, int, int) {
	if n < 3 {
		return n, r1, r2
	}
	last := b[n-1]
	if last != 'y' && last != 'Y' {
		return n, r1, r2
	}
	if isVowel(b[n-2]) {
		return n, r1, r2
	}
	b[n-1] = 'i'
	return n, r1, r2
}

func step2(b []byte, n, r1, r2 int) (int, int, int) {
	type pair struct {
		old, neu string
	}
	for _, p := range []pair{
		{"ational", "ate"},
		{"tional", "tion"},
		{"enci", "ence"},
		{"anci", "ance"},
		{"abli", "able"},
		{"entli", "ent"},
		{"ization", "ize"},
		{"izer", "ize"},
		{"ation", "ate"},
		{"ator", "ate"},
		{"alism", "al"},
		{"aliti", "al"},
		{"alli", "al"},
		{"fulness", "ful"},
		{"ousli", "ous"},
		{"ousness", "ous"},
		{"iveness", "ive"},
		{"iviti", "ive"},
		{"biliti", "ble"},
		{"bli", "ble"},
		{"fulli", "ful"},
		{"lessli", "less"},
	} {
		if ends(b, n, p.old) && inR(r1, n, p.old) {
			return replaceSuf(b, n, r1, r2, p.old, p.neu)
		}
	}
	if ends(b, n, "ogi") && inR(r1, n, "ogi") && n >= 4 && b[n-4] == 'l' {
		return replaceSuf(b, n, r1, r2, "ogi", "og")
	}
	if ends(b, n, "li") && inR(r1, n, "li") && n >= 3 && liEnding(b[n-3]) {
		return clip(n, r1, r2, 2)
	}
	return n, r1, r2
}

func liEnding(c byte) bool {
	switch c {
	case 'c', 'd', 'e', 'g', 'h', 'k', 'm', 'n', 'r', 't':
		return true
	}
	return false
}

func step3(b []byte, n, r1, r2 int) (int, int, int) {
	switch {
	case ends(b, n, "ational") && inR(r1, n, "ational"):
		return replaceSuf(b, n, r1, r2, "ational", "ate")
	case ends(b, n, "tional") && inR(r1, n, "tional"):
		return replaceSuf(b, n, r1, r2, "tional", "tion")
	case ends(b, n, "alize") && inR(r1, n, "alize"):
		return replaceSuf(b, n, r1, r2, "alize", "al")
	case ends(b, n, "icate") && inR(r1, n, "icate"):
		return replaceSuf(b, n, r1, r2, "icate", "ic")
	case ends(b, n, "iciti") && inR(r1, n, "iciti"):
		return replaceSuf(b, n, r1, r2, "iciti", "ic")
	case ends(b, n, "ical") && inR(r1, n, "ical"):
		return replaceSuf(b, n, r1, r2, "ical", "ic")
	case ends(b, n, "ful") && inR(r1, n, "ful"):
		return clip(n, r1, r2, 3)
	case ends(b, n, "ness") && inR(r1, n, "ness"):
		return clip(n, r1, r2, 4)
	case ends(b, n, "ative") && inR(r2, n, "ative"):
		return clip(n, r1, r2, 5)
	}
	return n, r1, r2
}

func step4(b []byte, n, r1, r2 int) (int, int, int) {
	for _, suf := range []string{
		"ement", "ance", "ence", "able", "ible", "ment",
		"ant", "ent", "ism", "ate", "iti", "ous", "ive", "ize",
		"al", "er", "ic",
	} {
		if ends(b, n, suf) && inR(r2, n, suf) {
			return clip(n, r1, r2, len(suf))
		}
	}
	if ends(b, n, "ion") && inR(r2, n, "ion") && n >= 4 {
		c := b[n-4]
		if c == 's' || c == 't' {
			return clip(n, r1, r2, 3)
		}
	}
	return n, r1, r2
}

func step5(b []byte, n, r1, r2 int) (int, int, int) {
	if n == 0 {
		return n, r1, r2
	}
	if b[n-1] == 'e' {
		if inR(r2, n, "e") || (inR(r1, n, "e") && !shortSyllable(b, n-1)) {
			return clip(n, r1, r2, 1)
		}
		return n, r1, r2
	}
	if b[n-1] == 'l' && inR(r2, n, "l") && n >= 2 && b[n-2] == 'l' {
		return clip(n, r1, r2, 1)
	}
	return n, r1, r2
}
