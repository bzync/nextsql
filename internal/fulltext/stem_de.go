package fulltext

// Snowball German stemmer, analyzer revision 1 (Snowball 3.x, including the
// merged german2 umlaut transliterations and apostrophe step).

func germanVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'y', 'ä', 'ö', 'ü':
		return true
	}
	return false
}

func germanSEnding(r rune) bool {
	switch r {
	case 'b', 'd', 'f', 'g', 'h', 'k', 'l', 'm', 'n', 'r', 't':
		return true
	}
	return false
}

func germanSTEnding(r rune) bool {
	switch r {
	case 'b', 'd', 'f', 'g', 'h', 'k', 'l', 'm', 'n', 't':
		return true
	}
	return false
}

func germanETEnding(r rune) bool {
	switch r {
	case 'd', 'f', 'g', 'k', 'l', 'm', 'n', 'r', 's', 't', 'U', 'z', 'ä':
		return true
	}
	return false
}

func stemGerman(term string) string {
	if term == "" {
		return term
	}
	for _, r := range term {
		if r >= '0' && r <= '9' {
			return term
		}
	}
	w := newStem(term, 16)
	germanPrelude(w)
	germanMarkRegions(w)
	germanStep1(w)
	germanStep2(w)
	germanStep3(w)
	germanStep4(w)
	germanPostlude(w)
	if w.n == 0 {
		return term
	}
	return w.str()
}

func germanPrelude(w *stemWord) {
	// Mark u/y between vowels first so "feuer" → "feUer" and is not later
	// rewritten as füer via the ue rule.
	for i := 1; i < w.n-1; i++ {
		if !germanVowel(w.r[i-1]) || !germanVowel(w.r[i+1]) {
			continue
		}
		switch w.r[i] {
		case 'u':
			w.r[i] = 'U'
		case 'y':
			w.r[i] = 'Y'
		}
	}
	i := 0
	for i < w.n {
		r := w.r[i]
		switch {
		case r == 'ß':
			w.r[i] = 's'
			w.insert(i+1, 's')
			i += 2
		case r == 'a' && i+1 < w.n && w.r[i+1] == 'e':
			w.r[i] = 'ä'
			// delete e
			copy(w.r[i+1:], w.r[i+2:w.n])
			w.n--
			i++
		case r == 'o' && i+1 < w.n && w.r[i+1] == 'e':
			w.r[i] = 'ö'
			copy(w.r[i+1:], w.r[i+2:w.n])
			w.n--
			i++
		case r == 'u' && i+1 < w.n && w.r[i+1] == 'e':
			// "qu" is left intact (quelle).
			if i > 0 && w.r[i-1] == 'q' {
				i++
				continue
			}
			w.r[i] = 'ü'
			copy(w.r[i+1:], w.r[i+2:w.n])
			w.n--
			i++
		case r == 'q' && i+1 < w.n && w.r[i+1] == 'u':
			i += 2
		default:
			i++
		}
	}
}

func germanMarkRegions(w *stemWord) {
	w.markR1R2(germanVowel)
	if w.r1 < 3 {
		w.r1 = 3
		if w.r1 > w.n {
			w.r1 = w.n
		}
	}
}

func germanStep1(w *stemWord) {
	suf := w.longest("erinnen", "erin", "lns", "ern", "em", "er", "en", "es", "ln", "e", "s")
	if suf == "" || !w.inRegion(w.r1, suf) {
		return
	}
	switch suf {
	case "em":
		if w.hasSuf("system") {
			return
		}
		w.delSuf(suf)
	case "ern", "er", "erin", "erinnen":
		w.delSuf(suf)
	case "e", "en", "es":
		w.delSuf(suf)
		if w.hasSuf("niss") {
			w.delSuf("s")
		}
	case "s":
		if w.n >= 2 && germanSEnding(w.r[w.n-2]) {
			w.delSuf("s")
		}
	case "ln", "lns":
		w.replaceSuf(suf, "l")
	}
}

func germanStep2(w *stemWord) {
	suf := w.longest("est", "en", "er", "st", "et")
	if suf == "" || !w.inRegion(w.r1, suf) {
		return
	}
	switch suf {
	case "en", "er", "est":
		w.delSuf(suf)
	case "st":
		// valid st-ending preceded by at least 3 letters.
		if w.n >= 6 && germanSTEnding(w.r[w.n-3]) {
			w.delSuf("st")
		}
	case "et":
		if w.n < 3 || !germanETEnding(w.r[w.n-3]) {
			return
		}
		stem := string(w.r[:w.n-2])
		if hasSuffix(stem, "geordn") || hasSuffix(stem, "intern") || hasSuffix(stem, "plan") || hasSuffix(stem, "tick") || hasSuffix(stem, "tr") {
			return
		}
		w.delSuf("et")
	}
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

func germanStep3(w *stemWord) {
	suf := w.longest("keit", "lich", "heit", "isch", "end", "ung", "ig", "ik")
	if suf == "" || !w.inRegion(w.r2, suf) {
		return
	}
	switch suf {
	case "end", "ung":
		w.delSuf(suf)
		if w.hasSuf("ig") && w.inRegion(w.r2, "ig") {
			prev := w.at(w.n - 3)
			if prev != 'e' {
				w.delSuf("ig")
			}
		}
	case "ig", "ik", "isch":
		prev := w.at(w.n - len([]rune(suf)) - 1)
		if prev != 'e' {
			w.delSuf(suf)
		}
	case "lich", "heit":
		w.delSuf(suf)
		if w.hasSuf("er") && w.inRegion(w.r1, "er") {
			w.delSuf("er")
		} else if w.hasSuf("en") && w.inRegion(w.r1, "en") {
			w.delSuf("en")
		}
	case "keit":
		w.delSuf(suf)
		if w.hasSuf("lich") && w.inRegion(w.r2, "lich") {
			w.delSuf("lich")
		} else if w.hasSuf("ig") && w.inRegion(w.r2, "ig") {
			w.delSuf("ig")
		}
	}
}

func germanStep4(w *stemWord) {
	// Remove 's, 'sch, or ' if at least two characters remain.
	switch {
	case w.hasSuf("'sch") && w.n-4 >= 2:
		w.delSuf("'sch")
	case w.hasSuf("'s") && w.n-2 >= 2:
		w.delSuf("'s")
	case w.hasSuf("'") && w.n-1 >= 2:
		w.delSuf("'")
	}
}

func germanPostlude(w *stemWord) {
	for i := 0; i < w.n; i++ {
		switch w.r[i] {
		case 'Y':
			w.r[i] = 'y'
		case 'U':
			w.r[i] = 'u'
		case 'ä':
			w.r[i] = 'a'
		case 'ö':
			w.r[i] = 'o'
		case 'ü':
			w.r[i] = 'u'
		}
	}
}
