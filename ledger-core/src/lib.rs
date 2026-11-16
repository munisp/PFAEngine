//! ledger-core: the correctness-critical hot path (AR-11).
//! Mirrors internal/ledger (Go) semantics: immutable double-entry postings,
//! integer minor units, deterministic replay, trial balance.

use std::collections::HashMap;

pub type Kobo = i64; // integer minor units; no floats (DA-03)

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum AccountType {
    Asset,
    Liability,
    Equity,
    Income,
    Expense,
    Suspense,
}

impl AccountType {
    pub fn normal_debit(&self) -> bool {
        matches!(self, AccountType::Asset | AccountType::Expense | AccountType::Suspense)
    }
}

#[derive(Clone, Debug)]
pub struct Account {
    pub id: String,
    pub tenant_id: String,
    pub name: String,
    pub kind: AccountType,
    pub currency: String,
}

#[derive(Clone, Debug)]
pub struct Posting {
    pub account_id: String,
    pub debit: Kobo,
    pub credit: Kobo,
}

#[derive(Clone, Debug)]
pub struct Entry {
    pub id: String,
    pub tenant_id: String,
    pub memo: String,
    pub reference: String,
    pub postings: Vec<Posting>,
    pub reversal_of: Option<String>,
}

#[derive(Debug, PartialEq)]
pub enum LedgerError {
    Unbalanced,
    UnknownAccount,
    DuplicateAccount,
    NotFound,
    SingleSidedViolation,
}

pub struct Ledger {
    accounts: HashMap<String, Account>,
    entries: Vec<Entry>,
    seq: u64,
}

impl Ledger {
    pub fn new() -> Self {
        Ledger { accounts: HashMap::new(), entries: Vec::new(), seq: 0 }
    }

    pub fn open_account(&mut self, a: Account) -> Result<(), LedgerError> {
        if self.accounts.contains_key(&a.id) {
            return Err(LedgerError::DuplicateAccount);
        }
        self.accounts.insert(a.id.clone(), a);
        Ok(())
    }

    /// Post an immutable double-entry entry. Must balance.
    pub fn post(
        &mut self,
        tenant_id: &str,
        memo: &str,
        reference: &str,
        postings: Vec<Posting>,
    ) -> Result<Entry, LedgerError> {
        let mut deb: i128 = 0;
        let mut cred: i128 = 0;
        for p in &postings {
            if !self.accounts.contains_key(&p.account_id) {
                return Err(LedgerError::UnknownAccount);
            }
            if p.debit < 0 || p.credit < 0 || (p.debit > 0 && p.credit > 0) {
                return Err(LedgerError::SingleSidedViolation);
            }
            deb += p.debit as i128;
            cred += p.credit as i128;
        }
        if deb != cred || deb == 0 {
            return Err(LedgerError::Unbalanced);
        }
        self.seq += 1;
        let e = Entry {
            id: format!("le-{}", self.seq),
            tenant_id: tenant_id.to_string(),
            memo: memo.to_string(),
            reference: reference.to_string(),
            postings,
            reversal_of: None,
        };
        self.entries.push(e.clone());
        Ok(e)
    }

    /// Reversal is the only "undo" (immutability, NFR-05).
    pub fn reverse(&mut self, entry_id: &str, memo: &str) -> Result<Entry, LedgerError> {
        let orig = self
            .entries
            .iter()
            .find(|e| e.id == entry_id)
            .cloned()
            .ok_or(LedgerError::NotFound)?;
        let rev: Vec<Posting> = orig
            .postings
            .iter()
            .map(|p| Posting { account_id: p.account_id.clone(), debit: p.credit, credit: p.debit })
            .collect();
        let mut e = self.post(&orig.tenant_id, memo, &format!("REV:{}", orig.reference), rev)?;
        e.reversal_of = Some(entry_id.to_string());
        Ok(e)
    }

    pub fn balance(&self, account_id: &str) -> Kobo {
        let a = match self.accounts.get(account_id) {
            Some(a) => a,
            None => return 0,
        };
        let mut bal: i128 = 0;
        for e in &self.entries {
            for p in &e.postings {
                if p.account_id != account_id {
                    continue;
                }
                if a.kind.normal_debit() {
                    bal += p.debit as i128 - p.credit as i128;
                } else {
                    bal += p.credit as i128 - p.debit as i128;
                }
            }
        }
        bal as Kobo
    }

    /// Deterministic replay: rebuild balances from the journal (FR-306).
    pub fn trial_balance(&self) -> HashMap<String, Kobo> {
        self.accounts.keys().map(|id| (id.clone(), self.balance(id))).collect()
    }

    pub fn entries(&self) -> &[Entry] {
        &self.entries
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn setup() -> Ledger {
        let mut l = Ledger::new();
        l.open_account(Account { id: "custody-cash".into(), tenant_id: "t1".into(), name: "Cash".into(), kind: AccountType::Asset, currency: "NGN".into() }).unwrap();
        l.open_account(Account { id: "member-rsa:m1".into(), tenant_id: "t1".into(), name: "RSA".into(), kind: AccountType::Liability, currency: "NGN".into() }).unwrap();
        l
    }

    #[test]
    fn rejects_unbalanced() {
        let mut l = setup();
        let r = l.post("t1", "bad", "r1", vec![
            Posting { account_id: "custody-cash".into(), debit: 10_000, credit: 0 },
            Posting { account_id: "member-rsa:m1".into(), debit: 0, credit: 9_900 },
        ]);
        assert_eq!(r.unwrap_err(), LedgerError::Unbalanced);
    }

    #[test]
    fn contribution_144000_ngn() {
        let mut l = setup();
        // ₦144,000 = 14,400,000 kobo (8% + 10% of ₦800,000)
        l.post("t1", "contribution", "sched-1", vec![
            Posting { account_id: "custody-cash".into(), debit: 14_400_000, credit: 0 },
            Posting { account_id: "member-rsa:m1".into(), debit: 0, credit: 14_400_000 },
        ]).unwrap();
        assert_eq!(l.balance("custody-cash"), 14_400_000);
        assert_eq!(l.balance("member-rsa:m1"), 14_400_000);
    }

    #[test]
    fn reversal_zeroes() {
        let mut l = setup();
        let e = l.post("t1", "c", "r", vec![
            Posting { account_id: "custody-cash".into(), debit: 100_000, credit: 0 },
            Posting { account_id: "member-rsa:m1".into(), debit: 0, credit: 100_000 },
        ]).unwrap();
        l.reverse(&e.id, "correction").unwrap();
        assert_eq!(l.balance("custody-cash"), 0);
        assert_eq!(l.balance("member-rsa:m1"), 0);
    }

    #[test]
    fn deterministic_replay() {
        let mut l = setup();
        for i in 0..10 {
            l.post("t1", "c", &format!("r{}", i), vec![
                Posting { account_id: "custody-cash".into(), debit: 1_000, credit: 0 },
                Posting { account_id: "member-rsa:m1".into(), debit: 0, credit: 1_000 },
            ]).unwrap();
        }
        let tb = l.trial_balance();
        assert_eq!(tb.get("custody-cash"), Some(&10_000));
    }
}
