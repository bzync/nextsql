package fulltext

// Snowball French stemmer, analyzer revision 1 (Snowball 3.x).
//
// Deterministic, locale-free, identical on every replica. Tokens that contain
// digits pass through unchanged.

func frenchVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'y',
		'â', 'à', 'ë', 'é', 'ê', 'è', 'ï', 'î', 'ô', 'û', 'ù':
		return true
	}
	return false
}

func frenchElide(term string) string {
	rs := []rune(term)
	n := len(rs)
	if n < 3 {
		return term
	}
	cut := 0
	if rs[0] == 'q' && rs[1] == 'u' && rs[2] == '\'' {
		cut = 3
	} else if n >= 2 && rs[1] == '\'' {
		switch rs[0] {
		case 'c', 'd', 'j', 'l', 'm', 'n', 's', 't', 'z':
			cut = 2
		}
	}
	if cut == 0 || cut >= n {
		return term
	}
	return string(rs[cut:])
}

func stemFrench(term string) string {
	if term == "" {
		return term
	}
	for _, r := range term {
		if r >= '0' && r <= '9' {
			return term
		}
	}
	term = frenchElide(term)
	if term == "" {
		return term
	}
	w := newStem(term, 16)
	frenchPrelude(w)
	frenchMarkRegions(w)
	before := w.str()
	step1 := frenchStandardSuffix(w)
	if !step1 || w.forceVerb {
		if !frenchIVerbSuffix(w) {
			frenchVerbSuffix(w)
		}
	}
	if w.str() != before {
		if w.last() == 'Y' {
			w.r[w.n-1] = 'i'
		} else if w.last() == 'ç' {
			w.r[w.n-1] = 'c'
		}
	} else {
		frenchResidual(w)
	}
	frenchUndouble(w)
	frenchUnaccent(w)
	frenchPostlude(w)
	if w.n == 0 {
		return term
	}
	return w.str()
}

func frenchPrelude(w *stemWord) {
	i := 0
	for i < w.n {
		r := w.r[i]
		switch r {
		case 'ë':
			w.r[i] = 'e'
			w.insert(i, 'H')
			i += 2
			continue
		case 'ï':
			w.r[i] = 'i'
			w.insert(i, 'H')
			i += 2
			continue
		}
		prevV := i > 0 && frenchVowel(w.r[i-1])
		nextV := i+1 < w.n && frenchVowel(w.r[i+1])
		switch r {
		case 'u':
			if (prevV && nextV) || (i > 0 && w.r[i-1] == 'q') {
				w.r[i] = 'U'
			}
		case 'i':
			if prevV && nextV {
				w.r[i] = 'I'
			}
		case 'y':
			if prevV || nextV {
				w.r[i] = 'Y'
			}
		}
		i++
	}
}

func frenchMarkRegions(w *stemWord) {
	w.markR1R2(frenchVowel)
	w.rv = w.n
	if w.n >= 3 && frenchVowel(w.r[0]) && frenchVowel(w.r[1]) {
		w.rv = 3
		return
	}
	if hasPrefixRunes(w, "par") || hasPrefixRunes(w, "col") || hasPrefixRunes(w, "tap") {
		w.rv = 3
		return
	}
	if w.n >= 3 && w.r[0] == 'n' && w.r[1] == 'i' && frenchVowel(w.r[2]) {
		w.rv = 3
		return
	}
	// First vowel not at the beginning of the word: skip first letter, gopast v.
	if w.n > 1 {
		w.rv = gopast(w.r[:w.n], 1, frenchVowel)
	}
}

func hasPrefixRunes(w *stemWord, p string) bool {
	rs := []rune(p)
	if w.n < len(rs) {
		return false
	}
	for i, r := range rs {
		if w.r[i] != r {
			return false
		}
	}
	return true
}

func frenchStandardSuffix(w *stemWord) bool {
	suf := frenchStep1Suffix(w)
	if suf == "" {
		return false
	}
	switch suf {
	case "ance", "iqUe", "isme", "able", "iste", "eux",
		"ances", "iqUes", "ismes", "ables", "istes":
		if w.inRegion(w.r2, suf) {
			w.delSuf(suf)
			return true
		}
		return false
	case "atrice", "ateur", "ation", "atrices", "ateurs", "ations":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		if w.hasSuf("ic") {
			if w.inRegion(w.r2, "ic") {
				w.delSuf("ic")
			} else {
				w.replaceSuf("ic", "iqU")
			}
		}
		return true
	case "logie", "logies":
		if w.inRegion(w.r2, suf) {
			w.replaceSuf(suf, "log")
			return true
		}
		return false
	case "usion", "ution", "usions", "utions":
		if w.inRegion(w.r2, suf) {
			w.replaceSuf(suf, "u")
			return true
		}
		return false
	case "ence", "ences":
		if w.inRegion(w.r2, suf) {
			w.replaceSuf(suf, "ent")
			return true
		}
		return false
	case "ement", "ements":
		if !w.inRegion(w.rv, suf) {
			return false
		}
		w.delSuf(suf)
		switch {
		case w.hasSuf("iv"):
			if w.inRegion(w.r2, "iv") {
				w.delSuf("iv")
				if w.hasSuf("at") && w.inRegion(w.r2, "at") {
					w.delSuf("at")
				}
			}
		case w.hasSuf("eus"):
			if w.inRegion(w.r2, "eus") {
				w.delSuf("eus")
			} else if w.inRegion(w.r1, "eus") {
				w.replaceSuf("eus", "eux")
			}
		case w.hasSuf("abl"):
			if w.inRegion(w.r2, "abl") {
				w.delSuf("abl")
			}
		case w.hasSuf("iqU"):
			if w.inRegion(w.r2, "iqU") {
				w.delSuf("iqU")
			}
		case w.hasSuf("ièr"):
			if w.inRegion(w.rv, "ièr") {
				w.replaceSuf("ièr", "i")
			}
		case w.hasSuf("Ièr"):
			if w.inRegion(w.rv, "Ièr") {
				w.replaceSuf("Ièr", "i")
			}
		}
		return true
	case "ité", "ités":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		switch {
		case w.hasSuf("abil"):
			if w.inRegion(w.r2, "abil") {
				w.delSuf("abil")
			} else {
				w.replaceSuf("abil", "abl")
			}
		case w.hasSuf("ic"):
			if w.inRegion(w.r2, "ic") {
				w.delSuf("ic")
			} else {
				w.replaceSuf("ic", "iqU")
			}
		case w.hasSuf("iv"):
			if w.inRegion(w.r2, "iv") {
				w.delSuf("iv")
			}
		}
		return true
	case "if", "ive", "ifs", "ives":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		if w.hasSuf("at") && w.inRegion(w.r2, "at") {
			w.delSuf("at")
			if w.hasSuf("ic") {
				if w.inRegion(w.r2, "ic") {
					w.delSuf("ic")
				} else {
					w.replaceSuf("ic", "iqU")
				}
			}
		}
		return true
	case "eaux":
		w.replaceSuf("eaux", "eau")
		return true
	case "aux":
		if w.inRegion(w.r1, "aux") {
			w.replaceSuf("aux", "al")
			return true
		}
		return false
	case "oux":
		if w.n >= 4 {
			switch w.r[w.n-4] {
			case 'b', 'h', 'j', 'l', 'n', 'p':
				w.replaceSuf("oux", "ou")
				return true
			}
		}
		return false
	case "euse", "euses":
		if w.inRegion(w.r2, suf) {
			w.delSuf(suf)
			return true
		}
		if w.inRegion(w.r1, suf) {
			w.replaceSuf(suf, "eux")
			return true
		}
		return false
	case "issement", "issements":
		if !w.inRegion(w.r1, suf) {
			return false
		}
		prev := w.n - len([]rune(suf)) - 1
		if prev < 0 || frenchVowel(w.r[prev]) {
			return false
		}
		w.delSuf(suf)
		return true
	case "amment":
		if !w.inRegion(w.rv, suf) {
			return false
		}
		w.replaceSuf(suf, "ant")
		w.forceVerb = true
		return true
	case "emment":
		if !w.inRegion(w.rv, suf) {
			return false
		}
		w.replaceSuf(suf, "ent")
		w.forceVerb = true
		return true
	case "ment", "ments":
		prev := w.n - len([]rune(suf)) - 1
		if prev < 0 || !frenchVowel(w.r[prev]) || prev < w.rv {
			return false
		}
		w.delSuf(suf)
		w.forceVerb = true
		return true
	}
	return false
}

func frenchStep1Suffix(w *stemWord) string {
	return w.longest(
		"issements", "issement",
		"atrices", "ateurs", "ations",
		"logies", "usions", "utions", "ements",
		"atrices",
		"amment", "emment",
		"euses", "ances", "iqUes", "ismes", "ables", "istes",
		"atrice", "ateur", "ation",
		"logie", "usion", "ution", "ement", "ences",
		"ments", "euse",
		"ance", "iqUe", "isme", "able", "iste",
		"eaux", "ités", "ives", "ence",
		"ité", "aux", "oux", "ifs", "ive", "eux",
		"ment", "if",
	)
}

func frenchIVerbSuffix(w *stemWord) bool {
	suf := w.longest(
		"issaIent", "issante", "issantes", "issants",
		"issions", "issiez", "issons", "issez", "issent", "isses", "isse",
		"issais", "issait", "issant",
		"iraIent", "irions", "iriez", "irons", "iront", "irez",
		"irais", "irait", "iras", "irent", "irai", "ira",
		"îmes", "îtes",
		"ies", "ie", "ir", "is", "it", "i",
		"ît",
	)
	if suf == "" || !w.inRegion(w.rv, suf) {
		return false
	}
	prev := w.n - len([]rune(suf)) - 1
	if prev < w.rv || prev < 0 {
		return false
	}
	r := w.r[prev]
	if frenchVowel(r) || r == 'H' {
		return false
	}
	w.delSuf(suf)
	return true
}

func frenchVerbSuffix(w *stemWord) bool {
	suf := w.longest(
		"eraIent", "erions", "eriez", "erons", "eront", "erez",
		"erais", "erait", "eras", "erai", "era",
		"èrent",
		"assions", "assiez", "assent", "asses", "asse",
		"antes", "ants", "ante", "aIent",
		"âmes", "âtes", "ât",
		"ions",
		"ées", "ée", "és", "é",
		"iez", "ez", "er",
		"ait", "ant", "ais",
		"ai", "as", "at", "a",
		"aises", "aise",
		"eais",
	)
	if suf == "" || !w.inRegion(w.rv, suf) {
		return false
	}
	switch suf {
	case "ions":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		return true
	case "é", "ée", "ées", "és", "èrent", "er", "era", "erai", "eraIent",
		"erais", "erait", "eras", "erez", "eriez", "erions", "erons", "eront",
		"ez", "iez":
		w.delSuf(suf)
		return true
	case "âmes", "ât", "âtes", "a", "ai", "aIent", "ait", "ant",
		"ante", "antes", "ants", "as", "asse", "assent", "asses", "assiez", "assions":
		w.delSuf(suf)
		if w.hasSuf("e") && w.inRegion(w.rv, "e") {
			w.delSuf("e")
		}
		return true
	case "ais", "aise", "aises":
		if frenchAisException(w, suf) {
			return false
		}
		w.delSuf(suf)
		return true
	case "eais":
		w.delSuf(suf)
		return true
	}
	return false
}

func frenchAisException(w *stemWord, suf string) bool {
	// Do not remove -ais/-aise/-aises from balais, mauvais, déplais, etc.
	stem := w.n - len([]rune(suf))
	if stem >= 2 && w.r[stem-2] == 'a' && w.r[stem-1] == 'l' && stem-2 == 1 {
		// 'al' preceded by exactly one character.
		return true
	}
	if stem >= 3 && w.r[stem-3] == 'a' && w.r[stem-2] == 'u' && w.r[stem-1] == 'v' {
		return true
	}
	if stem >= 3 && w.r[stem-3] == 'é' && w.r[stem-2] == 'p' && w.r[stem-1] == 'l' {
		return true
	}
	return false
}

func frenchResidual(w *stemWord) {
	if w.hasSuf("s") && w.n >= 2 {
		prev := w.r[w.n-2]
		hi := w.n >= 3 && w.r[w.n-3] == 'H' && prev == 'i'
		keep := false
		switch prev {
		case 'a', 'i', 'o', 'u', 'è', 's':
			keep = true
		}
		if hi || !keep {
			w.delSuf("s")
		}
	}
	suf := w.longest("ière", "Ière", "ier", "Ier", "ion", "e")
	if suf == "" || !w.inRegion(w.rv, suf) {
		return
	}
	switch suf {
	case "ion":
		if !w.inRegion(w.r2, "ion") {
			return
		}
		prev := w.at(w.n - 4)
		if prev == 's' || prev == 't' {
			w.delSuf("ion")
		}
	case "ier", "ière", "Ier", "Ière":
		w.replaceSuf(suf, "i")
	case "e":
		w.delSuf("e")
	}
}

func frenchUndouble(w *stemWord) {
	if w.hasSuf("enn") || w.hasSuf("onn") || w.hasSuf("ett") || w.hasSuf("ell") || w.hasSuf("eill") {
		w.clip(1)
	}
}

func frenchUnaccent(w *stemWord) {
	i := w.n - 1
	found := false
	for i >= 0 && !frenchVowel(w.r[i]) {
		found = true
		i--
	}
	if !found || i < 0 {
		return
	}
	switch w.r[i] {
	case 'é', 'è':
		w.r[i] = 'e'
	}
}

func frenchPostlude(w *stemWord) {
	out := make([]rune, 0, w.n)
	i := 0
	for i < w.n {
		r := w.r[i]
		switch r {
		case 'I':
			out = append(out, 'i')
		case 'U':
			out = append(out, 'u')
		case 'Y':
			out = append(out, 'y')
		case 'H':
			if i+1 < w.n && w.r[i+1] == 'e' {
				out = append(out, 'ë')
				i += 2
				continue
			}
			if i+1 < w.n && w.r[i+1] == 'i' {
				out = append(out, 'ï')
				i += 2
				continue
			}
			// drop remaining H
		default:
			out = append(out, r)
		}
		i++
	}
	w.grow(len(out))
	copy(w.r, out)
	w.n = len(out)
}
