package literacy

import (
	"encoding/json"
	"strconv"

	"github.com/munisp/pfaengine/internal/persist"
)

// Snapshot kinds owned by this engine.
const (
	kindLesson   = "literacy.lesson"
	kindProgress = "literacy.progress"
)

// globalTenant is the snapshot tenant for tenant-agnostic content (lessons
// are shared catalogue entries, not per-tenant state).
const globalTenant = "_global"

// lessonState is the persisted form of a Lesson. It mirrors Lesson but
// carries Answer, which the wire-facing JSON tag hides (`json:"-"`).
type lessonState struct {
	ID       string   `json:"id"`
	Lang     Lang     `json:"lang"`
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Answer   int      `json:"answer"`
}

func snapshotLesson(l *Lesson) lessonState {
	return lessonState{ID: l.ID, Lang: l.Lang, Topic: l.Topic, Title: l.Title,
		Body: l.Body, Question: l.Question, Options: l.Options, Answer: l.Answer}
}

// AttachSink wires write-through snapshot persistence (nil disables it,
// pure in-memory mode).
func (e *Engine) AttachSink(s persist.Sink) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink = s
}

// Restore rehydrates lessons and member progress from snapshots, ignoring
// foreign kinds. It performs no side-effects and does not re-persist. The
// ID sequence is advanced past every restored lesson ID so post-restore
// IDs never collide.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sn := range persist.OfKind(snaps, kindLesson) {
		var ls lessonState
		if err := json.Unmarshal(sn.Payload, &ls); err != nil {
			return err
		}
		bumpSeq(&e.seq, ls.ID)
		e.lessons[ls.ID] = &Lesson{ID: ls.ID, Lang: ls.Lang, Topic: ls.Topic, Title: ls.Title,
			Body: ls.Body, Question: ls.Question, Options: ls.Options, Answer: ls.Answer}
	}
	for _, sn := range persist.OfKind(snaps, kindProgress) {
		var p Progress
		if err := json.Unmarshal(sn.Payload, &p); err != nil {
			return err
		}
		e.progress[p.TenantID+"|"+p.MemberID+"|"+p.LessonID] = &p
	}
	return nil
}

// bumpSeq advances seq past the numeric suffix of id (e.g. "lit-000007" → 7)
// so freshly generated IDs never collide with restored ones.
func bumpSeq(seq *int, id string) {
	i := len(id)
	for i > 0 && id[i-1] >= '0' && id[i-1] <= '9' {
		i--
	}
	if i == len(id) {
		return
	}
	if n, err := strconv.Atoi(id[i:]); err == nil && n > *seq {
		*seq = n
	}
}
