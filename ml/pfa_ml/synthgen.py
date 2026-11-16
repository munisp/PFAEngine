"""Realistic Nigerian pension-fund synthetic data generator.

Distributions are shaped from public-domain knowledge of the Nigerian CPS:
  - Contributions: employer 10% + employee 8% of (basic+housing+transport),
    remitted 1-7 days after month end (PRA 2014 s.11 penalises lateness).
  - Salary bands skewed to Lagos/Abuja/Rivers; informal-sector micro-pension
    contributions are small and irregular.
  - Fraud archetypes calibrated to common PFA fraud patterns:
      A1 dormant-then-large (long-inactive RSA suddenly moved)
      A2 velocity spike (many transactions in minutes)
      A3 geo-jump (logins from distant states within an hour)
      A4 agent bust-out (field agent farms many new RSAs then drains)
      A5 account takeover (channel flip + beneficiary change + withdrawal)

Labels are ground truth for training. Amounts are integer kobo throughout.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import random
from dataclasses import dataclass, field, asdict
from pathlib import Path

NG_STATES = [
    "LA", "FC", "RI", "OY", "KN", "KD", "EN", "AN", "DE", "ED",
    "OG", "OS", "KW", "AB", "IM", "PL", "BY", "CR", "AK", "BO",
    "NI", "SO", "ZA", "KT", "JG", "YO", "TR", "AD", "GM", "NS",
    "EB", "EK", "ON", "BE", "KO", "BA",
]
STATE_GEO = {  # rough (lat, lon) for geo-jump distance
    "LA": (6.5, 3.4), "FC": (9.1, 7.5), "RI": (4.8, 7.0), "OY": (7.4, 3.9),
    "KN": (12.0, 8.5), "KD": (10.5, 7.4), "EN": (6.5, 7.5), "AN": (6.2, 7.0),
    "DE": (5.9, 5.7), "ED": (6.3, 5.6), "OG": (7.2, 3.4), "OS": (7.6, 4.6),
    "KW": (8.5, 4.5), "AB": (5.5, 7.5), "IM": (5.5, 7.0), "PL": (9.2, 9.5),
    "BY": (4.9, 6.3), "CR": (5.0, 8.3), "AK": (5.0, 7.9), "BO": (11.8, 13.2),
    "NI": (9.6, 6.6), "SO": (13.0, 5.2), "ZA": (12.2, 6.7), "KT": (12.9, 7.6),
    "JG": (11.7, 9.3), "YO": (12.3, 11.4), "TR": (7.9, 11.3), "AD": (9.2, 12.5),
    "GM": (10.3, 11.2), "NS": (8.5, 8.3), "EB": (6.3, 8.1), "EK": (7.7, 5.3),
    "ON": (7.5, 4.6), "BE": (7.3, 8.5), "KO": (7.8, 6.7), "BA": (10.3, 10.0),
}
CHANNELS = ["ussd", "mobile", "web", "branch", "agent"]
INDUSTRIES = ["banking", "telecom", "oil_gas", "govt", "education",
              "health", "manufacturing", "retail", "informal"]


def _haversine_km(a: str, b: str) -> float:
    import math
    (la1, lo1), (la2, lo2) = STATE_GEO[a], STATE_GEO[b]
    r, dlat, dlon = 6371.0, math.radians(la2 - la1), math.radians(lo2 - lo1)
    h = math.sin(dlat / 2) ** 2 + math.cos(math.radians(la1)) * math.cos(
        math.radians(la2)) * math.sin(dlon / 2) ** 2
    return 2 * r * math.asin(math.sqrt(h))


@dataclass
class Member:
    member_id: str
    rsa_pin: str
    state: str
    salary_monthly_kobo: int
    employer_id: str
    agent_id: str
    device_id: str
    micro_pension: bool
    age: int


@dataclass
class Txn:
    txn_id: str
    member_id: str
    kind: str          # contribution | withdrawal | transfer | inquiry | beneficiary_change
    amount_kobo: int
    channel: str
    state: str
    day: int           # day index in simulation
    minute_of_day: int
    device_id: str
    is_fraud: int = 0
    archetype: str = ""


@dataclass
class Generator:
    seed: int = 42
    n_members: int = 2000
    n_months: int = 18
    fraud_rate: float = 0.02
    rng: random.Random = field(init=False)

    def __post_init__(self):
        self.rng = random.Random(self.seed)
        self.members: list[Member] = []
        self.txns: list[Txn] = []
        self._txn_n = 0

    # ---------- population ----------
    def _gen_members(self) -> None:
        r = self.rng
        n_employers = max(10, self.n_members // 25)
        n_agents = max(5, self.n_members // 60)
        weights = [0.22 if s == "LA" else 0.12 if s == "FC" else 0.06 if s == "RI"
                   else 0.02 for s in NG_STATES]
        for i in range(self.n_members):
            micro = r.random() < 0.18
            if micro:
                salary = r.randint(30_000, 120_000) * 100  # kobo
            else:
                salary = int(r.lognormvariate(13.0, 0.55)) * 100
                salary = min(max(salary, 70_000 * 100), 3_000_000 * 100)
            self.members.append(Member(
                member_id=f"mem-{i:06d}", rsa_pin=f"PEN{2_000_000_000 + i}",
                state=r.choices(NG_STATES, weights=weights)[0],
                salary_monthly_kobo=salary,
                employer_id=f"emp-{r.randrange(n_employers):05d}",
                agent_id=f"agt-{r.randrange(n_agents):04d}",
                # legit members have their own device; fraud rings share
                device_id=f"dev-m{i:07d}",
                micro_pension=micro, age=r.randint(22, 59)))

    def _next_txn_id(self) -> str:
        self._txn_n += 1
        return f"txn-{self._txn_n:08d}"

    # ---------- normal behaviour ----------
    def _normal_flow(self, dormant_after: dict[str, int] | None = None) -> None:
        r = self.rng
        dormant_after = dormant_after or {}
        for m in self.members:
            stop = dormant_after.get(m.member_id)
            last_day = -10_000
            for month in range(self.n_months):
                if m.micro_pension and r.random() < 0.45:
                    continue  # irregular micro contributions
                pay_day = month * 30 + r.randint(1, 7)
                if stop is not None and pay_day >= stop:
                    continue  # member has gone dormant (A1 precursor)
                amt = m.salary_monthly_kobo * (8 + 10) // 100 if not m.micro_pension \
                    else r.randint(2_000, 30_000) * 100
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "contribution", amt,
                                     r.choice(["web", "mobile", "branch"]),
                                     m.state, pay_day, r.randint(480, 1020),
                                     m.device_id))
                last_day = pay_day
                # occasional withdrawals (retirees / 25% mortgage equity)
                if r.random() < 0.015:
                    wamt = int(m.salary_monthly_kobo * r.uniform(0.5, 6.0))
                    self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                         "withdrawal", wamt,
                                         r.choice(["branch", "web"]),
                                         m.state, pay_day + r.randint(3, 25),
                                         r.randint(540, 960), m.device_id))
                if r.random() < 0.10:
                    self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                         "inquiry", 0, r.choice(CHANNELS),
                                         m.state, pay_day + r.randint(0, 28),
                                         r.randint(0, 1439), m.device_id))
            m_age_days = last_day  # stored for dormancy calc
            m.__dict__["_last_day"] = m_age_days

    # ---------- fraud archetypes ----------
    def _assign_fraud(self) -> tuple[list[tuple], dict[str, int]]:
        """Pick victims + archetypes + fraud day up-front so the normal flow
        can stop contributions for A1 (dormant-then-large) victims first."""
        r = self.rng
        n_fraud = int(self.n_members * self.fraud_rate)
        victims = r.sample(self.members, n_fraud)
        archetypes = ["A1", "A2", "A3", "A4", "A5"]
        assigns, dormant_after = [], {}
        for vi, m in enumerate(victims):
            kind = r.choice(archetypes)
            day = r.randint(self.n_months * 30 // 2, self.n_months * 30 - 2)
            if kind == "A1":
                stop = max(0, day - r.randint(90, 220))
                dormant_after[m.member_id] = stop
            assigns.append((vi, m, kind, day))
        return assigns, dormant_after

    def _inject_fraud(self, assigns: list[tuple]) -> None:
        r = self.rng
        for vi, m, kind, day in assigns:
            # fraud rings reuse devices across victims (bust-out / ATO rings)
            ring_dev = f"dev-ring-{vi // 3:04d}"
            if kind == "A1":  # dormant-then-large
                big = int(m.salary_monthly_kobo * r.uniform(4, 9))
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "withdrawal", big, "ussd",
                                     m.state, day, r.randint(0, 1439),
                                     ring_dev, 1, "A1"))
            elif kind == "A2":  # velocity spike
                base_min = r.randint(0, 1400)
                for k in range(r.randint(5, 9)):
                    self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                         r.choice(["withdrawal", "transfer"]),
                                         int(m.salary_monthly_kobo * 0.3),
                                         "mobile", m.state, day,
                                         base_min + k * 2, m.device_id, 1, "A2"))
            elif kind == "A3":  # geo-jump
                far = max(NG_STATES, key=lambda s: _haversine_km(m.state, s))
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "inquiry", 0, "mobile", m.state, day,
                                     700, m.device_id, 0))
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "withdrawal",
                                     int(m.salary_monthly_kobo * 1.5),
                                     "mobile", far, day, 745, m.device_id,
                                     1, "A3"))
            elif kind == "A4":  # agent bust-out: agent's device across many RSAs
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "withdrawal",
                                     int(m.salary_monthly_kobo * 2.0), "agent",
                                     m.state, day, 600,
                                     f"dev-agent-{m.agent_id}", 1, "A4"))
            else:  # A5 account takeover
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "beneficiary_change", 0, "web",
                                     m.state, day, 200, ring_dev, 1, "A5"))
                self.txns.append(Txn(self._next_txn_id(), m.member_id,
                                     "withdrawal",
                                     int(m.salary_monthly_kobo * 3.0), "web",
                                     m.state, day, 260, ring_dev, 1, "A5"))

    # ---------- run ----------
    def run(self, out: Path) -> dict:
        out.mkdir(parents=True, exist_ok=True)
        self._gen_members()
        assigns, dormant_after = self._assign_fraud()
        self._normal_flow(dormant_after)
        self._inject_fraud(assigns)
        self.txns.sort(key=lambda t: (t.day, t.minute_of_day))
        members_p = out / "members.jsonl"
        txns_p = out / "transactions.jsonl"
        with open(members_p, "w") as f:
            for m in self.members:
                d = asdict(m); d.pop("_last_day", None)
                f.write(json.dumps(d) + "\n")
        with open(txns_p, "w") as f:
            for t in self.txns:
                f.write(json.dumps(asdict(t)) + "\n")
        h = hashlib.sha256()
        for p in (members_p, txns_p):
            h.update(p.read_bytes())
        manifest = {"seed": self.seed, "members": len(self.members),
                    "transactions": len(self.txns),
                    "fraud_txns": sum(t.is_fraud for t in self.txns),
                    "dataset_sha256": h.hexdigest()}
        (out / "manifest.json").write_text(json.dumps(manifest, indent=2))
        return manifest


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="/tmp/pfa-data")
    ap.add_argument("--members", type=int, default=2000)
    ap.add_argument("--months", type=int, default=18)
    ap.add_argument("--seed", type=int, default=42)
    args = ap.parse_args()
    g = Generator(seed=args.seed, n_members=args.members, n_months=args.months)
    print(json.dumps(g.run(Path(args.out)), indent=2))


if __name__ == "__main__":
    main()
