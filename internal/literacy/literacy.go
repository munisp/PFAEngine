// Package literacy implements the localized financial-literacy engine:
// bite-size pension lessons and quizzes in English, Nigerian Pidgin,
// Hausa, Yoruba and Igbo, with per-member progress that feeds the
// chatbot/WhatsApp nudge channels.
package literacy

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Lang is a supported content language.
type Lang string

const (
	LangEN Lang = "en"  // English
	LangPC Lang = "pcm" // Nigerian Pidgin
	LangHA Lang = "ha"  // Hausa
	LangYO Lang = "yo"  // Yoruba
	LangIG Lang = "ig"  // Igbo
)

var (
	ErrNoLesson  = errors.New("literacy: lesson not found")
	ErrBadAnswer = errors.New("literacy: option index out of range")
)

// Lesson is one localized lesson with a quiz.
type Lesson struct {
	ID       string   `json:"id"`
	Lang     Lang     `json:"lang"`
	Topic    string   `json:"topic"` // e.g. "AVC", "MPG", "multi-fund"
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Answer   int      `json:"-"` // correct option index (not exposed in lists)
}

// Progress is a member's completion state for a lesson.
type Progress struct {
	TenantID    string    `json:"tenant_id"`
	MemberID    string    `json:"member_id"`
	LessonID    string    `json:"lesson_id"`
	Passed      bool      `json:"passed"`
	CompletedAt time.Time `json:"completed_at"`
}

// Engine holds the content catalogue and member progress.
type Engine struct {
	mu       sync.Mutex
	lessons  map[string]*Lesson
	progress map[string]*Progress // tenantID|memberID|lessonID
	seq      int
}

func NewEngine() *Engine {
	return &Engine{lessons: map[string]*Lesson{}, progress: map[string]*Progress{}}
}

// AddLesson registers content.
func (e *Engine) AddLesson(lang Lang, topic, title, body, question string, options []string, answer int) Lesson {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	l := &Lesson{ID: fmt.Sprintf("lit-%06d", e.seq), Lang: lang, Topic: topic, Title: title,
		Body: body, Question: question, Options: options, Answer: answer}
	e.lessons[l.ID] = l
	return *l
}

// Catalogue lists lessons, optionally filtered by lang/topic. Answers
// are stripped.
func (e *Engine) Catalogue(lang Lang, topic string) []Lesson {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Lesson
	for _, l := range e.lessons {
		if (lang == "" || l.Lang == lang) && (topic == "" || l.Topic == topic) {
			out = append(out, *l)
		}
	}
	return out
}

// Answer checks a member's quiz answer and records progress.
func (e *Engine) Answer(tenantID, memberID, lessonID string, option int, at time.Time) (Progress, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.lessons[lessonID]
	if !ok {
		return Progress{}, ErrNoLesson
	}
	if option < 0 || option >= len(l.Options) {
		return Progress{}, ErrBadAnswer
	}
	p := &Progress{TenantID: tenantID, MemberID: memberID, LessonID: lessonID,
		Passed: option == l.Answer, CompletedAt: at}
	e.progress[tenantID+"|"+memberID+"|"+lessonID] = p
	return *p, nil
}

// Score returns passed/total counts for a member.
func (e *Engine) Score(tenantID, memberID string) (passed, total int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	prefix := tenantID + "|" + memberID + "|"
	for k, p := range e.progress {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			total++
			if p.Passed {
				passed++
			}
		}
	}
	return
}

// Seed ships the launch catalogue (EN + PCM for the three highest-impact
// topics; other languages added by content ops through AddLesson).
func (e *Engine) Seed() {
	e.AddLesson(LangEN, "MPG", "The ₦52,500 floor", "If your monthly pension is below ₦52,500 the Minimum Pension Guarantee tops it up from the Pension Protection Fund.", "Who funds the MPG top-up?", []string{"Your employer only", "The Pension Protection Fund", "Your bank"}, 1)
	e.AddLesson(LangPC, "MPG", "Di ₦52,500 floor", "If your pension small pass ₦52,500, government go top am up from di Pension Protection Fund.", "Who dey pay di top-up?", []string{"Na your oga only", "Na di Pension Protection Fund", "Na your bank"}, 1)
	e.AddLesson(LangEN, "AVC", "Save more, retire better", "Additional Voluntary Contributions grow your RSA faster and can be withdrawn partially before retirement under PenCom rules.", "What does AVC stand for?", []string{"Additional Voluntary Contribution", "Annual Verified Credit", "Approved Vendor Charge"}, 0)
	e.AddLesson(LangPC, "AVC", "Add small-small, rest well-well", "AVC na extra money wey you dey add to your RSA; e dey grow your retirement money quick.", "Wetin AVC mean?", []string{"Additional Voluntary Contribution", "Annual Verified Credit", "Approved Vendor Charge"}, 0)
	e.AddLesson(LangEN, "multi-fund", "Your fund changes as you age", "Under 50 you are in Fund II (or Fund I if you opt in); at 50 you move to Fund III; retirees use Fund IV.", "Which fund is for retirees?", []string{"Fund I", "Fund II", "Fund IV"}, 2)
	e.AddLesson(LangPC, "multi-fund", "Your fund dey change as you dey old", "If you never reach 50 you dey Fund II; from 50 na Fund III; retirees na Fund IV.", "Which fund na for retirees?", []string{"Fund I", "Fund II", "Fund IV"}, 2)
}
