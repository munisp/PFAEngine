// Package ledger implements FR-302 / FR-306: immutable double-entry
// postings, integer minor units, deterministic replay, trial balance.
// Event-sourced: postings are append-only; balances are projections.
// Rust hot-path mirror: ledger-core/ (same semantics).
package ledger

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrUnbalanced   = errors.New("ledger: entry does not balance")
	ErrAccountExist = errors.New("ledger: account exists")
	ErrNoAccount    = errors.New("ledger: unknown account")
	ErrImmutable    = errors.New("ledger: entries are immutable; post a reversal")
)

type AccountType string

const (
	Asset     AccountType = "ASSET"
	Liability AccountType = "LIABILITY"
	Equity    AccountType = "EQUITY"
	Income    AccountType = "INCOME"
	Expense   AccountType = "EXPENSE"
	Suspense  AccountType = "SUSPENSE"
)

// Normal debit balance for Asset/Expense/Suspense; credit otherwise.
func (t AccountType) NormalDebit() bool {
	return t == Asset || t == Expense || t == Suspense
}

type Account struct {
	ID       string      `json:"id"`
	TenantID string      `json:"tenant_id"`
	Name     string      `json:"name"`
	Type     AccountType `json:"type"`
	Currency string      `json:"currency"`
}

type Posting struct {
	AccountID string     `json:"account_id"`
	Debit     money.Kobo `json:"debit"`
	Credit    money.Kobo `json:"credit"`
}

type Entry struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Memo       string    `json:"memo"`
	Reference  string    `json:"reference"`
	Postings   []Posting `json:"postings"`
	PostedAt   time.Time `json:"posted_at"`
	ReversalOf string    `json:"reversal_of,omitempty"`
}

// Ledger is an append-only double-entry journal per tenant scope.
type Ledger struct {
	mu       sync.Mutex
	accounts map[string]Account
	entries  []Entry
	seq      int
}

func New() *Ledger { return &Ledger{accounts: map[string]Account{}} }

func (l *Ledger) OpenAccount(a Account) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.accounts[a.ID]; ok {
		return ErrAccountExist
	}
	l.accounts[a.ID] = a
	return nil
}

// Post validates double-entry balance and appends immutably.
func (l *Ledger) Post(tenantID, memo, reference string, postings []Posting) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var deb, cred int64
	for _, p := range postings {
		if _, ok := l.accounts[p.AccountID]; !ok {
			return Entry{}, fmt.Errorf("%w: %s", ErrNoAccount, p.AccountID)
		}
		if p.Debit < 0 || p.Credit < 0 || (p.Debit > 0 && p.Credit > 0) {
			return Entry{}, errors.New("ledger: posting must be single-sided and non-negative")
		}
		deb += int64(p.Debit)
		cred += int64(p.Credit)
	}
	if deb != cred || deb == 0 {
		return Entry{}, ErrUnbalanced
	}
	l.seq++
	e := Entry{
		ID: fmt.Sprintf("le-%d", l.seq), TenantID: tenantID, Memo: memo,
		Reference: reference, Postings: postings, PostedAt: time.Now().UTC(),
	}
	l.entries = append(l.entries, e)
	return e, nil
}

// Reverse posts a compensating entry (the only way to "undo", ErrImmutable semantics).
func (l *Ledger) Reverse(entryID, actorMemo string) (Entry, error) {
	l.mu.Lock()
	var orig *Entry
	for i := range l.entries {
		if l.entries[i].ID == entryID {
			orig = &l.entries[i]
			break
		}
	}
	l.mu.Unlock()
	if orig == nil {
		return Entry{}, ErrNoAccount
	}
	rev := make([]Posting, len(orig.Postings))
	for i, p := range orig.Postings {
		rev[i] = Posting{AccountID: p.AccountID, Debit: p.Credit, Credit: p.Debit}
	}
	e, err := l.Post(orig.TenantID, actorMemo, "REV:"+orig.Reference, rev)
	if err != nil {
		return Entry{}, err
	}
	e.ReversalOf = entryID
	return e, nil
}

// Balance computes the signed balance of an account (debit-normal positive).
func (l *Ledger) Balance(accountID string) money.Kobo {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.accounts[accountID]
	if !ok {
		return 0
	}
	var bal int64
	for _, e := range l.entries {
		for _, p := range e.Postings {
			if p.AccountID != accountID {
				continue
			}
			if a.Type.NormalDebit() {
				bal += int64(p.Debit) - int64(p.Credit)
			} else {
				bal += int64(p.Credit) - int64(p.Debit)
			}
		}
	}
	return money.Kobo(bal)
}

// TrialBalance returns all account balances; sum of debit-normal equals
// sum of credit-normal (zero-unexplained-difference rule, §4 ledger).
func (l *Ledger) TrialBalance() map[string]money.Kobo {
	l.mu.Lock()
	ids := make([]string, 0, len(l.accounts))
	for id := range l.accounts {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	sort.Strings(ids)
	out := map[string]money.Kobo{}
	for _, id := range ids {
		out[id] = l.Balance(id)
	}
	return out
}

// Entries returns the append-only journal (regulator replay, FR-306).
func (l *Ledger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Entry{}, l.entries...)
}

// Replay rebuilds all balances from the journal — deterministic replay.
func (l *Ledger) Replay() map[string]money.Kobo { return l.TrialBalance() }
