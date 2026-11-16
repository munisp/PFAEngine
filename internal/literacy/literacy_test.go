package literacy

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestSeedAndAnswer(t *testing.T) {
	e := NewEngine()
	e.Seed()
	cat := e.Catalogue(LangPC, "")
	if len(cat) != 3 {
		t.Fatalf("pidgin catalogue: %d", len(cat))
	}
	catEN := e.Catalogue(LangEN, "MPG")
	if len(catEN) != 1 {
		t.Fatalf("en mpg: %d", len(catEN))
	}
	// Correct answer passes.
	p, err := e.Answer("t1", "m1", catEN[0].ID, 1, now)
	if err != nil || !p.Passed {
		t.Fatalf("answer: %v %+v", err, p)
	}
	// Wrong answer recorded as failed.
	p, err = e.Answer("t1", "m1", catEN[0].ID, 0, now)
	if err != nil || p.Passed {
		t.Fatalf("wrong: %v %+v", err, p)
	}
	passed, total := e.Score("t1", "m1")
	if total != 1 || passed != 0 {
		t.Fatalf("score: %d/%d", passed, total)
	}
}

func TestValidation(t *testing.T) {
	e := NewEngine()
	e.Seed()
	if _, err := e.Answer("t1", "m1", "nope", 0, now); err != ErrNoLesson {
		t.Fatalf("lesson: %v", err)
	}
	l := e.Catalogue("", "")[0]
	if _, err := e.Answer("t1", "m1", l.ID, 99, now); err != ErrBadAnswer {
		t.Fatalf("option: %v", err)
	}
}
