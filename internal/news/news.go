// Package news implements the announcements / pension-news CMS that PFA
// websites run (Tranche 21): PenCom notices, industry updates, product
// announcements and education pieces, with pinning and draft/publish
// lifecycle. Surfaced in the member PWA, Flutter and via the chatbot.
package news

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/persist"
)

// Category classifies articles.
type Category string

const (
	CatPenComNotice Category = "PENCOM_NOTICE"
	CatIndustry     Category = "INDUSTRY"
	CatProduct      Category = "PRODUCT"
	CatEducation    Category = "EDUCATION"
)

// Status is the publication lifecycle.
type Status string

const (
	Draft     Status = "DRAFT"
	Published Status = "PUBLISHED"
	Archived  Status = "ARCHIVED"
)

// Article is one CMS entry.
type Article struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Category    Category   `json:"category"`
	Pinned      bool       `json:"pinned"`
	Status      Status     `json:"status"`
	Author      string     `json:"author"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// Engine is the tenant-scoped CMS.
type Engine struct {
	mu       sync.Mutex
	articles map[string]*Article
	seq      int
	sink     persist.Sink // nil in pure in-memory mode
}

// New returns an empty engine.
func New() *Engine { return &Engine{articles: map[string]*Article{}} }

// AttachSink wires write-through snapshot persistence (NFR-10).
func (e *Engine) AttachSink(s persist.Sink) { e.sink = s }

// Restore rehydrates articles from boot snapshots; foreign kinds ignored.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range persist.OfKind(snaps, "news.article") {
		var a Article
		if err := json.Unmarshal(s.Payload, &a); err != nil {
			return err
		}
		ac := a
		e.articles[a.ID] = &ac
		if n, err := strconv.Atoi(strings.TrimPrefix(a.ID, "art-")); err == nil && n > e.seq {
			e.seq = n
		}
	}
	return nil
}

// Create drafts an article.
func (e *Engine) Create(tenantID, title, body string, cat Category, author string, pinned bool) (Article, error) {
	if tenantID == "" || title == "" || body == "" {
		return Article{}, errors.New("news: tenant, title and body required")
	}
	switch cat {
	case CatPenComNotice, CatIndustry, CatProduct, CatEducation:
	default:
		return Article{}, fmt.Errorf("news: unsupported category %s", cat)
	}
	e.mu.Lock()
	e.seq++
	a := &Article{
		ID: fmt.Sprintf("art-%05d", e.seq), TenantID: tenantID, Title: title,
		Body: body, Category: cat, Pinned: pinned, Status: Draft,
		Author: author, CreatedAt: time.Now(),
	}
	e.articles[a.ID] = a
	out := *a
	e.mu.Unlock()
	persist.Save(e.sink, "news.article", tenantID, out.ID, out)
	return out, nil
}

// Publish releases a draft.
func (e *Engine) Publish(tenantID, id string) (Article, error) {
	e.mu.Lock()
	a, ok := e.articles[id]
	if !ok || a.TenantID != tenantID {
		e.mu.Unlock()
		return Article{}, fmt.Errorf("news: article %s not found", id)
	}
	if a.Status != Draft {
		e.mu.Unlock()
		return Article{}, errors.New("news: only drafts can be published")
	}
	now := time.Now()
	a.Status, a.PublishedAt = Published, &now
	out := *a
	e.mu.Unlock()
	persist.Save(e.sink, "news.article", tenantID, out.ID, out)
	return out, nil
}

// Archive withdraws an article from the public feed.
func (e *Engine) Archive(tenantID, id string) (Article, error) {
	e.mu.Lock()
	a, ok := e.articles[id]
	if !ok || a.TenantID != tenantID {
		e.mu.Unlock()
		return Article{}, fmt.Errorf("news: article %s not found", id)
	}
	a.Status = Archived
	out := *a
	e.mu.Unlock()
	persist.Save(e.sink, "news.article", tenantID, out.ID, out)
	return out, nil
}

// Feed returns published articles (pinned first, then newest). Category
// filter optional.
func (e *Engine) Feed(tenantID string, cat Category) []Article {
	e.mu.Lock()
	var out []Article
	for _, a := range e.articles {
		if a.TenantID != tenantID || a.Status != Published {
			continue
		}
		if cat != "" && a.Category != cat {
			continue
		}
		out = append(out, *a)
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].PublishedAt.After(*out[j].PublishedAt)
	})
	return out
}

// Drafts lists unpublished articles (staff view).
func (e *Engine) Drafts(tenantID string) []Article {
	e.mu.Lock()
	var out []Article
	for _, a := range e.articles {
		if a.TenantID == tenantID && a.Status == Draft {
			out = append(out, *a)
		}
	}
	e.mu.Unlock()
	return out
}
