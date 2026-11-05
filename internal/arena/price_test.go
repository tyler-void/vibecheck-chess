package arena

import (
	"math"
	"sync"
	"testing"
)

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestPriceCost(t *testing.T) {
	p := Price{Input: 4, Output: 20, CacheRead: 0.20, CacheWrite: 5}
	u := Usage{In: 1500, Out: 800, CacheRead: 2000, CacheWrite: 0}
	// 1500*4 + 800*20 + 2000*0.2 = 6000 + 16000 + 400 = 22400 per 1e6
	approx(t, p.Cost(u), 0.0224)
}

func TestPriceCostCacheWrite(t *testing.T) {
	p := Price{Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.125}
	u := Usage{In: 100, Out: 50, CacheWrite: 2000}
	approx(t, p.Cost(u), (100*0.10+50*0.50+2000*0.125)/1e6)
}

func TestPriceCostZero(t *testing.T) {
	approx(t, Price{Input: 4, Output: 20}.Cost(Usage{}), 0)
}

func TestPriceLongPromptStep(t *testing.T) {
	p := Price{Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.125,
		LongPrompt: &LongPrompt{AboveInputTokens: 100_000, Multiplier: 5}}
	at := Usage{In: 100_000, Out: 1000}
	approx(t, p.Cost(at), (100_000*0.10+1000*0.50)/1e6) // at the threshold: no step
	above := Usage{In: 90_000, CacheRead: 10_001, Out: 1000}
	approx(t, p.Cost(above), 5*(90_000*0.10+10_001*0.01+1000*0.50)/1e6)
}

func TestPricesFor(t *testing.T) {
	ps := Prices{"claude-haiku-5-5": {Input: 0.1}}
	if _, err := ps.For("claude-haiku-5-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.For("claude-opus-5-5"); err == nil {
		t.Fatal("want error for model without price")
	}
}

func TestUsageAdd(t *testing.T) {
	got := Usage{1, 2, 3, 4}.Add(Usage{10, 20, 30, 40})
	if got != (Usage{11, 22, 33, 44}) {
		t.Fatalf("got %+v", got)
	}
}

func TestBudgetTryStart(t *testing.T) {
	b := NewBudget(10)
	if !b.TryStart("g1", 4) {
		t.Fatal("g1 should start: 0+4 <= 10")
	}
	if !b.TryStart("g2", 4) {
		t.Fatal("g2 should start: 4+4 <= 10")
	}
	if b.TryStart("g3", 4) {
		t.Fatal("g3 must not start: 8+4 > 10")
	}
	if !b.TryStart("g3", 2) {
		t.Fatal("g3 should start at the cap exactly: 8+2 <= 10")
	}
	if b.TryStart("g4", 0.01) {
		t.Fatal("cap reached")
	}
}

func TestBudgetInflightUsesMaxOfSpentAndPrior(t *testing.T) {
	b := NewBudget(10)
	b.TryStart("g1", 2)
	b.Charge("g1", 5) // in-flight estimate now max(5, 2) = 5
	if b.TryStart("g2", 6) {
		t.Fatal("5 + 6 > 10")
	}
	if !b.TryStart("g2", 5) {
		t.Fatal("5 + 5 <= 10")
	}
	if got := b.Spent(); got != 5 {
		t.Fatalf("spent %v", got)
	}
}

func TestBudgetFinishReleasesEstimate(t *testing.T) {
	b := NewBudget(10)
	b.TryStart("g1", 8)
	b.Charge("g1", 1)
	b.Finish("g1") // only the real 1 USD remains
	if !b.TryStart("g2", 9) {
		t.Fatal("1 + 9 <= 10 after finish")
	}
}

func TestBudgetChargeAfterCapStillCounts(t *testing.T) {
	b := NewBudget(1)
	b.TryStart("g1", 0.5)
	b.Charge("g1", 3) // in-flight games may overrun; they are allowed to finish
	if got := b.Spent(); got != 3 {
		t.Fatalf("spent %v", got)
	}
}

func TestBudgetConcurrent(t *testing.T) {
	b := NewBudget(1e9)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i%26))
			b.TryStart(id, 1)
			for range 100 {
				b.Charge(id, 0.01)
			}
		}(i)
	}
	wg.Wait()
	approx(t, math.Round(b.Spent()*100)/100, 50)
}
