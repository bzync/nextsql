package fulltext

// Snowball Spanish stemmer, analyzer revision 1 (Snowball 3.x).

func spanishVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'á', 'é', 'í', 'ó', 'ú', 'ü':
		return true
	}
	return false
}

func stemSpanish(term string) string {
	if term == "" {
		return term
	}
	for _, r := range term {
		if r >= '0' && r <= '9' {
			return term
		}
	}
	w := newStem(term, 8)
	spanishMarkRegions(w)
	spanishAttachedPronoun(w)
	if !spanishStandardSuffix(w) {
		if !spanishYVerbSuffix(w) {
			spanishVerbSuffix(w)
		}
	}
	spanishResidual(w)
	spanishPostlude(w)
	if w.n == 0 {
		return term
	}
	return w.str()
}

func spanishMarkRegions(w *stemWord) {
	w.markR1R2(spanishVowel)
	w.rv = w.n
	if w.n < 2 {
		return
	}
	v0 := spanishVowel(w.r[0])
	v1 := spanishVowel(w.r[1])
	switch {
	case v0 && !v1:
		// vowel-consonant: RV after the next vowel
		w.rv = gopast(w.r[:w.n], 2, spanishVowel)
	case v0 && v1:
		// two vowels: RV after the next consonant
		w.rv = gopast(w.r[:w.n], 2, func(r rune) bool { return !spanishVowel(r) })
	case !v0 && !v1:
		// two consonants: RV after the next vowel
		w.rv = gopast(w.r[:w.n], 2, spanishVowel)
	default:
		// consonant-vowel: RV after the third letter
		if w.n >= 3 {
			w.rv = 3
		}
	}
}

func spanishAttachedPronoun(w *stemWord) {
	pron := w.longest("selas", "selos", "sela", "selo", "las", "les", "los", "nos", "la", "le", "lo", "me", "se")
	if pron == "" {
		return
	}
	stem := w.n - len([]rune(pron))
	saved := w.n
	w.n = stem
	suf := w.longest("iéndo", "ándo", "yendo", "ando", "iendo", "ár", "ér", "ír", "ar", "er", "ir")
	w.n = saved
	if suf == "" {
		return
	}
	sufStart := stem - len([]rune(suf))
	switch suf {
	case "yendo":
		if sufStart < w.rv {
			return
		}
		if sufStart < 1 || w.r[sufStart-1] != 'u' {
			return
		}
		w.delSuf(pron)
	case "iéndo", "ándo", "ár", "ér", "ír":
		if sufStart < w.rv {
			return
		}
		w.delSuf(pron)
		w.replaceSuf(suf, stripSpanishAcuteSuffix(suf))
	default:
		if sufStart < w.rv {
			return
		}
		w.delSuf(pron)
	}
}

func stripSpanishAcuteSuffix(suf string) string {
	switch suf {
	case "iéndo":
		return "iendo"
	case "ándo":
		return "ando"
	case "ár":
		return "ar"
	case "ér":
		return "er"
	case "ír":
		return "ir"
	}
	return suf
}

func spanishStandardSuffix(w *stemWord) bool {
	suf := w.longest(
		"amientos", "imientos", "amiento", "imiento",
		"aciones", "adoras", "adores", "ancias", "ación", "acion",
		"logías", "logía", "uciones", "ución", "ucion",
		"encias", "encia", "amente",
		"idades", "idad", "antes", "ancia", "ante", "adora", "ador",
		"istas", "ismos", "ables", "ibles", "anzas", "mente",
		"istas", "ista", "ismo", "able", "ible", "anza",
		"icos", "icas", "icos", "osa", "oso", "osos", "osas",
		"ico", "ica",
		"ivas", "ivos", "iva", "ivo",
	)
	if suf == "" {
		return false
	}
	switch suf {
	case "anza", "anzas", "ico", "ica", "icos", "icas", "ismo", "ismos",
		"able", "ables", "ible", "ibles", "ista", "istas", "oso", "osa",
		"osos", "osas", "amiento", "amientos", "imiento", "imientos":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		return true
	case "adora", "ador", "ación", "adoras", "adores", "aciones",
		"ante", "antes", "ancia", "ancias", "acion":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		if w.hasSuf("ic") && w.inRegion(w.r2, "ic") {
			w.delSuf("ic")
		}
		return true
	case "logía", "logías":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.replaceSuf(suf, "log")
		return true
	case "ución", "uciones", "ucion":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.replaceSuf(suf, "u")
		return true
	case "encia", "encias":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.replaceSuf(suf, "ente")
		return true
	case "amente":
		if !w.inRegion(w.r1, suf) {
			return false
		}
		w.delSuf(suf)
		switch {
		case w.hasSuf("iv") && w.inRegion(w.r2, "iv"):
			w.delSuf("iv")
			if w.hasSuf("at") && w.inRegion(w.r2, "at") {
				w.delSuf("at")
			}
		case w.hasSuf("os") && w.inRegion(w.r2, "os"):
			w.delSuf("os")
		case w.hasSuf("ic") && w.inRegion(w.r2, "ic"):
			w.delSuf("ic")
		case w.hasSuf("ad") && w.inRegion(w.r2, "ad"):
			w.delSuf("ad")
		}
		return true
	case "mente":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		if w.hasSuf("ante") && w.inRegion(w.r2, "ante") {
			w.delSuf("ante")
		} else if w.hasSuf("able") && w.inRegion(w.r2, "able") {
			w.delSuf("able")
		} else if w.hasSuf("ible") && w.inRegion(w.r2, "ible") {
			w.delSuf("ible")
		}
	case "idad", "idades":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		if w.hasSuf("abil") && w.inRegion(w.r2, "abil") {
			w.delSuf("abil")
		} else if w.hasSuf("ic") && w.inRegion(w.r2, "ic") {
			w.delSuf("ic")
		} else if w.hasSuf("iv") && w.inRegion(w.r2, "iv") {
			w.delSuf("iv")
		}
		return true
	case "iva", "ivo", "ivas", "ivos":
		if !w.inRegion(w.r2, suf) {
			return false
		}
		w.delSuf(suf)
		if w.hasSuf("at") && w.inRegion(w.r2, "at") {
			w.delSuf("at")
		}
		return true
	}
	return suf == "mente"
}

func spanishYVerbSuffix(w *stemWord) bool {
	suf := w.longest("yeron", "yendo", "yamos", "yais", "yan", "yen", "yas", "yes", "ya", "ye", "yo", "yó")
	if suf == "" || !w.inRegion(w.rv, suf) {
		return false
	}
	prev := w.n - len([]rune(suf)) - 1
	if prev < 0 || w.r[prev] != 'u' {
		return false
	}
	w.delSuf(suf)
	return true
}

func spanishVerbSuffix(w *stemWord) bool {
	// Longest-first among RV verb suffixes.
	suf := w.longest(spanishVerbSuffixes...)
	if suf == "" || !w.inRegion(w.rv, suf) {
		return false
	}
	switch suf {
	case "en", "es", "éis", "emos":
		w.delSuf(suf)
		if w.hasSuf("u") && w.n >= 2 && w.r[w.n-2] == 'g' {
			w.delSuf("u")
		}
		return true
	default:
		w.delSuf(suf)
		return true
	}
}

var spanishVerbSuffixes = []string{
	"aríamos", "eríamos", "iríamos", "iésemos", "iéramos", "ásemos", "áramos",
	"aríais", "eríais", "iríais", "asteis", "isteis", "arais", "ierais",
	"aseis", "ieseis", "ábamos", "íamos",
	"arían", "arías", "erían", "erías", "irían", "irías",
	"arán", "arás", "erán", "erás", "irán", "irás",
	"aría", "eréis", "aréis", "iréis", "ería", "iría",
	"aremos", "eremos", "iremos",
	"aban", "ían", "aran", "ieran", "asen", "iesen", "aron", "ieron",
	"ando", "iendo", "adas", "idas", "abas", "ías", "aras", "ieras",
	"ases", "ieses", "abais", "íais", "ados", "idos", "amos", "imos",
	"ará", "aré", "erá", "eré", "irá", "iré",
	"aba", "ada", "ida", "ía", "ara", "iera", "ase", "iese",
	"aste", "iste", "ado", "ido", "ió",
	"áis", "éis", "éis",
	"ad", "ed", "id", "an", "ió", "ar", "er", "ir", "as", "ís",
	"en", "es", "emos",
}

func spanishResidual(w *stemWord) {
	suf := w.longest("os", "á", "í", "ó", "a", "o", "é", "e")
	if suf == "" || !w.inRegion(w.rv, suf) {
		return
	}
	switch suf {
	case "os", "a", "o", "á", "í", "ó":
		w.delSuf(suf)
	case "e", "é":
		w.delSuf(suf)
		if w.hasSuf("u") && w.inRegion(w.rv, "u") && w.n >= 2 && w.r[w.n-2] == 'g' {
			w.delSuf("u")
		}
	}
}

func spanishPostlude(w *stemWord) {
	for i := 0; i < w.n; i++ {
		switch w.r[i] {
		case 'á':
			w.r[i] = 'a'
		case 'é':
			w.r[i] = 'e'
		case 'í':
			w.r[i] = 'i'
		case 'ó':
			w.r[i] = 'o'
		case 'ú':
			w.r[i] = 'u'
		}
	}
}
