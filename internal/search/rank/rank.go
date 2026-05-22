package rank

type Candidate struct {
	ChunkID string
	Score   float64
	Source  string
}

func RRF(lists [][]Candidate, k float64) map[string]float64 {
	if k <= 0 {
		k = 60
	}
	scores := map[string]float64{}
	for _, list := range lists {
		for i, candidate := range list {
			scores[candidate.ChunkID] += 1.0 / (k + float64(i+1))
		}
	}
	return scores
}
