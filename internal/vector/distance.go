package vector

import (
	"math"

	"github.com/bzync/nextsql/internal/bitvec"
	"github.com/bzync/nextsql/internal/nerr"
)

// Metric is a vector distance. Lower is closer.
type Metric uint8

const (
	MetricInvalid Metric = iota
	MetricCosine
	MetricL2
	MetricIP
	// MetricHamming is the number of differing elements. It is the natural
	// distance for a BITVECTOR<N> column (0/1 vectors); on such vectors it
	// equals the L1 distance.
	MetricHamming
)

func (m Metric) String() string {
	switch m {
	case MetricCosine:
		return "cosine"
	case MetricL2:
		return "l2"
	case MetricIP:
		return "inner_product"
	case MetricHamming:
		return "hamming"
	default:
		return "invalid"
	}
}

// ParseMetric maps SQL USING / function names. Empty is COSINE.
func ParseMetric(s string) (Metric, error) {
	switch s {
	case "", "cosine":
		return MetricCosine, nil
	case "l2":
		return MetricL2, nil
	case "inner_product":
		return MetricIP, nil
	case "hamming":
		return MetricHamming, nil
	default:
		return MetricInvalid, nerr.New(nerr.InvalidArgument, "vector.ParseMetric", "unknown distance metric")
	}
}

// CosineSimilarity is a·b / (|a||b|). Zero-norm vectors yield 0.
func CosineSimilarity(a, b []float32) float64 {
	dot, na, nb := dots(a, b)
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (na * nb)
}

// L2 is Euclidean distance.
func L2(a, b []float32) float64 {
	var s float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		d := float64(a[i]) - float64(b[i])
		s += d * d
	}
	return math.Sqrt(s)
}

// InnerProduct is a·b.
func InnerProduct(a, b []float32) float64 {
	dot, _, _ := dots(a, b)
	return dot
}

// Distance is lower-is-closer for every metric.
// COSINE: 1 − similarity. L2: Euclidean. INNER_PRODUCT: −dot. HAMMING: differing
// element count.
func Distance(m Metric, a, b []float32) float64 {
	switch m {
	case MetricL2:
		return L2(a, b)
	case MetricIP:
		return -InnerProduct(a, b)
	case MetricHamming:
		return bitvec.Hamming(a, b)
	default:
		return 1 - CosineSimilarity(a, b)
	}
}

// Similarity is the natural function value (not the ranking distance). For
// HAMMING there is no separate similarity; the differing-element count is
// returned.
func Similarity(m Metric, a, b []float32) float64 {
	switch m {
	case MetricL2:
		return L2(a, b)
	case MetricIP:
		return InnerProduct(a, b)
	case MetricHamming:
		return bitvec.Hamming(a, b)
	default:
		return CosineSimilarity(a, b)
	}
}

func dots(a, b []float32) (dot, na, nb float64) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	return dot, math.Sqrt(na), math.Sqrt(nb)
}

// SameDim reports whether a and b have equal length.
func SameDim(a, b []float32) bool { return len(a) == len(b) }

// Norm is the Euclidean norm of v.
func Norm(v []float32) (float64, error) {
	if err := Check(v, 0); err != nil {
		return 0, err
	}
	var sum float64
	for _, x := range v {
		f := float64(x)
		sum += f * f
	}
	return math.Sqrt(sum), nil
}

// Normalize returns a unit-length copy. A zero vector has no direction and is
// rejected rather than silently producing NaN or an arbitrary result.
func Normalize(v []float32) ([]float32, error) {
	norm, err := Norm(v)
	if err != nil {
		return nil, err
	}
	if norm == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "vector.Normalize", "cannot normalize a zero vector")
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	if err := Check(out, len(v)); err != nil {
		return nil, err
	}
	return out, nil
}

// Add and Sub perform element-wise arithmetic with strict dimension checks.
func Add(a, b []float32) ([]float32, error) { return combine(a, b, false) }
func Sub(a, b []float32) ([]float32, error) { return combine(a, b, true) }

func combine(a, b []float32, subtract bool) ([]float32, error) {
	if err := Check(a, 0); err != nil {
		return nil, err
	}
	if err := Check(b, len(a)); err != nil {
		return nil, err
	}
	out := make([]float32, len(a))
	for i := range a {
		if subtract {
			out[i] = a[i] - b[i]
		} else {
			out[i] = a[i] + b[i]
		}
	}
	if err := Check(out, len(a)); err != nil {
		return nil, err
	}
	return out, nil
}

// Scale multiplies every component by a finite scalar.
func Scale(v []float32, scalar float64) ([]float32, error) {
	if err := Check(v, 0); err != nil {
		return nil, err
	}
	if math.IsNaN(scalar) || math.IsInf(scalar, 0) {
		return nil, nerr.New(nerr.InvalidArgument, "vector.Scale", "scale is not finite")
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) * scalar)
	}
	if err := Check(out, len(v)); err != nil {
		return nil, err
	}
	return out, nil
}

// L1 is Manhattan distance with strict dimension checks.
func L1(a, b []float32) (float64, error) {
	if err := Check(a, 0); err != nil {
		return 0, err
	}
	if err := Check(b, len(a)); err != nil {
		return 0, err
	}
	var sum float64
	for i := range a {
		sum += math.Abs(float64(a[i]) - float64(b[i]))
	}
	return sum, nil
}
