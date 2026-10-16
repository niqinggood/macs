package memory

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type WeightedFact struct {
	Fact
	Score float64 `json:"score"`
}

// Decay computes an exponential recency score using the current time.
func Decay(createdAt string, halfLifeDays float64) float64 {
	return DecayAt(createdAt, halfLifeDays, time.Now())
}

// DecayAt is the deterministic form used by weighting and tests.
func DecayAt(createdAt string, halfLifeDays float64, now time.Time) float64 {
	if createdAt == "" {
		return 0.5
	}
	created, err := time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		created, err = time.Parse("2006-01-02", createdAt)
		if err != nil {
			return 0.5
		}
	}
	days := now.Sub(created).Hours() / 24
	if days < 0 {
		days = 0
	}
	if halfLifeDays <= 0 {
		halfLifeDays = 7
	}
	return math.Exp(-math.Ln2 * days / halfLifeDays)
}

func WeightFacts(facts []Fact, limit int, now time.Time) []WeightedFact {
	weighted := make([]WeightedFact, 0, len(facts))
	for _, fact := range facts {
		halfLife := 7.0
		switch fact.Predicate {
		case "preference", "prefers", "dislikes", "loves":
			halfLife = 30
		case "is_a", "contains", "imports", "calls", "method_of":
			halfLife = 90
		case "project_path", "workspace":
			halfLife = 365
		}
		score := DecayAt(fact.CreatedAt, halfLife, now)
		if fact.Source == "conversation" && strings.Contains(fact.Subject, "用户") {
			score *= 1.5
		}
		weighted = append(weighted, WeightedFact{Fact: fact, Score: score})
	}
	sort.SliceStable(weighted, func(i, j int) bool {
		return weighted[i].Score > weighted[j].Score
	})
	if limit > 0 && len(weighted) > limit {
		weighted = weighted[:limit]
	}
	return weighted
}

type FactFilter func(Fact) bool

// BuildProactiveMemory formats recent, confident and visible facts for prompt
// injection. At least three facts are required to avoid noisy reminders.
func BuildProactiveMemory(facts []Fact, now time.Time, visible, suppress FactFilter) string {
	cutoff := now.AddDate(0, 0, -30).Format("2006-01-02")
	highValue := make([]WeightedFact, 0, len(facts))
	for _, fact := range facts {
		if suppress != nil && suppress(fact) {
			continue
		}
		if visible != nil && !visible(fact) {
			continue
		}
		if fact.CreatedAt < cutoff || fact.Confidence < 0.8 {
			continue
		}
		score := DecayAt(fact.CreatedAt, 7, now)
		if score > 0.3 {
			highValue = append(highValue, WeightedFact{Fact: fact, Score: score})
		}
	}
	if len(highValue) < 3 {
		return ""
	}
	sort.SliceStable(highValue, func(i, j int) bool {
		return highValue[i].Score > highValue[j].Score
	})
	if len(highValue) > 8 {
		highValue = highValue[:8]
	}

	var output strings.Builder
	output.WriteString("## Recent Context (from memory)\n")
	output.WriteString("Things you might want to know:\n")
	for _, weighted := range highValue {
		prefix := "🟢"
		if weighted.Score > 0.7 {
			prefix = "🔴"
		} else if weighted.Score > 0.5 {
			prefix = "🟡"
		}
		fmt.Fprintf(&output, "%s %s → %s → %s\n", prefix, weighted.Subject, weighted.Predicate, weighted.Object)
	}
	return output.String()
}
