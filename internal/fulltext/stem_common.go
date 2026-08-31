package fulltext

// stemWord is a rune buffer for Snowball stemmers. Regions r1/r2/rv are
// offsets into r[:n]; a suffix is "in" a region when its first rune is at or
// after that offset.
type stemWord struct {
	r          []rune
	n          int
	r1, r2, rv int
	forceVerb  bool
}

func newStem(s string, extra int) *stemWord {
	rs := []rune(s)
	if extra < 8 {
		extra = 8
	}
	buf := make([]rune, len(rs)+extra)
	copy(buf, rs)
	n := len(rs)
	return &stemWord{r: buf, n: n, r1: n, r2: n, rv: n}
}

func (w *stemWord) hasSuf(suf string) bool {
	rs := []rune(suf)
	if w.n < len(rs) {
		return false
	}
	off := w.n - len(rs)
	for i, r := range rs {
		if w.r[off+i] != r {
			return false
		}
	}
	return true
}

func (w *stemWord) inRegion(region int, suf string) bool {
	return w.n-len([]rune(suf)) >= region
}

func (w *stemWord) sufIn(region int, suf string) bool {
	return w.hasSuf(suf) && w.inRegion(region, suf)
}

func (w *stemWord) clip(k int) {
	if k > w.n {
		k = w.n
	}
	if k < 0 {
		return
	}
	w.n -= k
}

func (w *stemWord) delSuf(suf string) {
	w.clip(len([]rune(suf)))
}

func (w *stemWord) grow(need int) {
	if need <= len(w.r) {
		return
	}
	nbuf := make([]rune, need+8)
	copy(nbuf, w.r[:w.n])
	w.r = nbuf
}

func (w *stemWord) addSuf(s string) {
	rs := []rune(s)
	w.grow(w.n + len(rs))
	copy(w.r[w.n:], rs)
	w.n += len(rs)
}

func (w *stemWord) replaceSuf(old, neu string) {
	w.delSuf(old)
	if neu != "" {
		w.addSuf(neu)
	}
}

func (w *stemWord) insert(i int, rs ...rune) {
	if i < 0 {
		i = 0
	}
	if i > w.n {
		i = w.n
	}
	w.grow(w.n + len(rs))
	copy(w.r[i+len(rs):], w.r[i:w.n])
	copy(w.r[i:], rs)
	w.n += len(rs)
}

func (w *stemWord) at(i int) rune {
	if i < 0 || i >= w.n {
		return 0
	}
	return w.r[i]
}

func (w *stemWord) last() rune {
	if w.n == 0 {
		return 0
	}
	return w.r[w.n-1]
}

func (w *stemWord) str() string {
	return string(w.r[:w.n])
}

// longest returns the longest matching suffix among sufs, or "".
func (w *stemWord) longest(sufs ...string) string {
	best := ""
	bestn := 0
	for _, s := range sufs {
		n := len([]rune(s))
		if n > bestn && w.hasSuf(s) {
			best = s
			bestn = n
		}
	}
	return best
}

func (w *stemWord) endsWith(rs ...rune) bool {
	if w.n < len(rs) {
		return false
	}
	off := w.n - len(rs)
	for i, r := range rs {
		if w.r[off+i] != r {
			return false
		}
	}
	return true
}

// markR1R2 sets R1/R2 as Snowball's gopast-vowel / gopast-non-vowel regions.
func (w *stemWord) markR1R2(vowel func(rune) bool) {
	w.r1 = gopast(w.r[:w.n], 0, vowel)
	w.r1 = gopast(w.r[:w.n], w.r1, func(r rune) bool { return !vowel(r) })
	w.r2 = gopast(w.r[:w.n], w.r1, vowel)
	w.r2 = gopast(w.r[:w.n], w.r2, func(r rune) bool { return !vowel(r) })
}

// gopast advances i past the first rune matching f, Snowball-style:
// skip while !f, then skip that matching rune. Returns n when the grouping
// cannot be found (region is empty at the end).
func gopast(b []rune, i int, f func(rune) bool) int {
	n := len(b)
	if i < 0 {
		i = 0
	}
	for i < n && !f(b[i]) {
		i++
	}
	if i >= n {
		return n
	}
	return i + 1
}
