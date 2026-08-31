package fulltext

import (
	"bytes"
	"math"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	// K1 is the BM25 term-frequency saturation parameter.
	K1 = 1.2
	// B is the BM25 document-length normalization parameter.
	B = 0.75
)

// IDF is Lucene-style BM25 inverse document frequency:
// ln(1 + (N - df + 0.5) / (df + 0.5)).
func IDF(n, df uint64) float64 {
	if n == 0 || df == 0 {
		return 0
	}
	return math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
}

// AvgDL is tokens / docs. Zero when the corpus is empty.
func AvgDL(st Stats) float64 {
	if st.Docs == 0 {
		return 0
	}
	return float64(st.Tokens) / float64(st.Docs)
}

// Score is the BM25 contribution of one term in one document.
func Score(tf, dl uint32, avgdl, idf float64) float64 {
	return ScoreTF(float64(tf), dl, avgdl, idf)
}

// ScoreTF is Score with a floating term frequency so field weights can
// scale tf without changing k1 or b.
func ScoreTF(tf float64, dl uint32, avgdl, idf float64) float64 {
	if tf <= 0 || idf == 0 {
		return 0
	}
	if avgdl <= 0 {
		avgdl = 1
	}
	if dl == 0 {
		dl = 1
	}
	denom := tf + K1*(1-B+B*float64(dl)/avgdl)
	if denom == 0 {
		return 0
	}
	return idf * (tf * (K1 + 1) / denom)
}

// CheckFieldWeight rejects a non-finite, non-positive, or oversized SEARCH
// field weight.
func CheckFieldWeight(w float64) error {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 || w > MaxFieldWeight {
		return nerr.New(nerr.InvalidArgument, "fulltext.CheckFieldWeight", "field weight out of range")
	}
	return nil
}

// Hit is one scored document.
type Hit struct {
	PK    []byte
	Score float64
}

// LessHit orders by score descending, then primary key for determinism.
func LessHit(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return bytes.Compare(a.PK, b.PK) < 0
}
