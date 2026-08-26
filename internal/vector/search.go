package vector

import (
	"sort"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
)

// Candidate is one vector considered by exact flat search.
type Candidate struct {
	PK  []byte
	Vec []float32
}

// FlatSearch returns the k closest candidates. k < 1 means every candidate.
// Distances run on the scheduler when workers > 1.
func FlatSearch(query []float32, metric Metric, cands []Candidate, k, workers int) ([]Hit, error) {
	if err := Check(query, 0); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}
	for i := range cands {
		if err := Check(cands[i].Vec, len(query)); err != nil {
			return nil, err
		}
		if len(cands[i].PK) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "vector.FlatSearch", "empty primary key")
		}
	}
	if k < 1 || k > len(cands) {
		k = len(cands)
	}
	hits := make([]Hit, len(cands))
	if workers < 1 {
		workers = 1
	}
	if workers == 1 || len(cands) < 64 {
		for i, c := range cands {
			hits[i] = Hit{PK: append([]byte(nil), c.PK...), Dist: Distance(metric, query, c.Vec)}
		}
	} else {
		chunk := (len(cands) + workers - 1) / workers
		tasks := make([]func() error, 0, workers)
		for start := 0; start < len(cands); start += chunk {
			start := start
			end := start + chunk
			if end > len(cands) {
				end = len(cands)
			}
			tasks = append(tasks, func() error {
				for i := start; i < end; i++ {
					hits[i] = Hit{PK: append([]byte(nil), cands[i].PK...), Dist: Distance(metric, query, cands[i].Vec)}
				}
				return nil
			})
		}
		if err := scheduler.DefaultPool.Run(nil, workers, tasks); err != nil {
			return nil, err
		}
	}
	sort.Slice(hits, func(i, j int) bool { return LessHit(hits[i], hits[j]) })
	return hits[:k], nil
}
