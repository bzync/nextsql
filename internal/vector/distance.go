package vector

import (
	"math"

	"github.com/bzync/nextsql/internal/nerr"
)

// Metric is a vector distance. Lower is closer.
type Metric uint8

const (
	MetricInvalid Metric = iota
	MetricCosine
	MetricL2
	MetricIP
)

func (m Metric) String() string {
	switch m {
	case MetricCosine:
		return "cosine"
	case MetricL2:
		return "l2"
	case MetricIP:
		return "inner_product"
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
// COSINE: 1 − similarity. L2: Euclidean. INNER_PRODUCT: −dot.
func Distance(m Metric, a, b []float32) float64 {
	switch m {
	case MetricL2:
		return L2(a, b)
	case MetricIP:
		return -InnerProduct(a, b)
	default:
		return 1 - CosineSimilarity(a, b)
	}
}

// Similarity is the natural function value (not the ranking distance).
func Similarity(m Metric, a, b []float32) float64 {
	switch m {
	case MetricL2:
		return L2(a, b)
	case MetricIP:
		return InnerProduct(a, b)
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
