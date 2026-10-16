package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type DreamKnowledge interface {
	DurableEntityNames() ([]string, error)
	MergeEntities(canonical, alias string) error
	RecentFacts(limit int) ([]Fact, error)
}

type FactVisibility func(Fact) bool

type DreamerDependencies struct {
	Knowledge   DreamKnowledge
	CallLLM     LLMCaller
	FactVisible FactVisibility
}

// Dreamer periodically digests stored memories during idle time.
// Inspired by Honcho's Dreamer module — background processing that
// builds user representations without consuming chat tokens.
type Dreamer struct {
	knowledge DreamKnowledge
	callLLM   LLMCaller
	visible   FactVisibility
	interval  time.Duration
	stopCh    chan struct{}
	mu        sync.Mutex
	running   bool
}

func NewDreamer(dependencies DreamerDependencies) *Dreamer {
	if dependencies.Knowledge == nil || dependencies.CallLLM == nil {
		return nil
	}
	return &Dreamer{
		knowledge: dependencies.Knowledge,
		callLLM:   dependencies.CallLLM,
		visible:   dependencies.FactVisible,
		interval:  5 * time.Minute,
		stopCh:    make(chan struct{}),
	}
}

// Start begins background memory digestion.
func (d *Dreamer) Start() {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.stopCh = make(chan struct{})
	d.running = true
	stopCh := d.stopCh
	d.mu.Unlock()

	go func() {
		initialDelay := time.NewTimer(30 * time.Second)
		select {
		case <-stopCh:
			if !initialDelay.Stop() {
				<-initialDelay.C
			}
			return
		case <-initialDelay.C:
		}
		log.Printf("[DREAMER] started (interval: %v)", d.interval)
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				log.Printf("[DREAMER] stopped")
				return
			case <-ticker.C:
				d.digest()
			}
		}
	}()
}

func (d *Dreamer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		close(d.stopCh)
		d.running = false
	}
}

// digest runs one cycle of memory digestion.
func (d *Dreamer) digest() {
	log.Printf("[DREAMER] digesting...")

	// 1. Entity dedup — merge similar entity names
	merged := d.dedupEntities()
	if merged > 0 {
		log.Printf("[DREAMER] merged %d entity collisions", merged)
	}

	// 2. Discover insights from recent memory patterns
	insights := d.discoverInsights()
	if len(insights) > 0 {
		for _, ins := range insights {
			log.Printf("[DREAMER] insight: %s", ins)
		}
	}
}

// dedupEntities scans and merges similar entity names.
func (d *Dreamer) dedupEntities() int {
	entities, err := d.knowledge.DurableEntityNames()
	if err != nil {
		return 0
	}

	merged := 0
	seen := make(map[string]string) // lower → canonical

	for _, e := range entities {
		lower := strings.ToLower(strings.TrimSpace(e))
		if canonical, exists := seen[lower]; exists {
			// Merge: alias e → canonical
			if canonical != e {
				if err := d.knowledge.MergeEntities(canonical, e); err == nil {
					merged++
				}
			}
		} else {
			seen[lower] = e
		}
	}

	return merged
}

// discoverInsights uses LLM to find patterns in recent memories.
func (d *Dreamer) discoverInsights() []string {
	facts, err := d.knowledge.RecentFacts(100)
	if err != nil || len(facts) < 5 {
		return nil
	}

	// Build a compact summary of recent facts
	var sb strings.Builder
	for _, f := range facts {
		if d.visible != nil && !d.visible(f) {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s → %s → %s\n", f.Subject, f.Predicate, f.Object))
	}
	if sb.Len() == 0 {
		return nil
	}

	prompt := fmt.Sprintf(`Analyze these remembered facts and generate 1-3 brief insights.
Focus on patterns, recurring themes, or things the user might want to follow up on.
Be concise — one sentence per insight. Use the user's language.

Facts:
%s

Output one insight per line, no numbering:`, sb.String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := d.callLLM(ctx, []map[string]interface{}{
		{"role": "system", "content": "You are an insight discovery system. Find patterns in remembered facts. Output ONE insight per line. No numbering, no commentary."},
		{"role": "user", "content": prompt},
	}, 256)
	if err != nil {
		return nil
	}

	// Parse insights (one per line)
	var insights []string
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && len(line) > 10 {
			insights = append(insights, line)
		}
	}
	return insights
}
