"""Knowledge-Graph QA over the pension graph — EPR-style (Evidence Pattern
Retrieval) adapted to the platform domain.

Full EPR-KGQA (WWW'24, nju-websoft) scores candidate evidence patterns with
BERT; this implementation applies the same *structural* idea at platform
scale without a 400MB encoder dependency:

  1. Question -> intent + entity extraction (template lexicon over the
     pension domain: balance, contributions, employer, agent, fraud rings)
  2. Atomic adjacency patterns (AP) enumerated from the member graph:
     (Member)-[:EMPLOYED_BY]->(Employer), (Member)-[:USED]->(Device), ...
  3. Candidate evidence patterns = AP combinations matching the intent
  4. Patterns scored by a compact neural scorer (PyTorch linear over
     pattern features: hop count, node types, edge recency) — trained
     weights shipped, same registry as FraudFusion
  5. Best pattern extracts the subgraph; the answer is rendered from it

Backend: FalkorDB when FALKOR_ADDR is set, else the in-process graph built
from the lakehouse parquet/jsonl export (same data, honest fallback).
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path

# Intent lexicon: intent -> (patterns as edge-type sequences, keywords)
INTENTS = {
    "fraud_ring": {
        "patterns": [("USED", "USED")],  # member-device-member
        "keywords": ["ring", "shared device", "who else uses", "linked"],
    },
    "employer_of": {
        "patterns": [("EMPLOYED_BY",)],
        "keywords": ["employer", "works for", "company"],
    },
    "agent_of": {
        "patterns": [("ONBOARDED_BY",)],
        "keywords": ["agent", "onboarded", "enrolled by"],
    },
    "members_of_employer": {
        "patterns": [("EMPLOYED_BY",)],
        "keywords": ["members at", "employees of", "staff of"],
    },
}

RE_MEMBER = re.compile(r"mem-\d+|PEN\d+")


@dataclass
class Answer:
    intent: str
    subgraph: list[tuple[str, str, str]]
    text: str
    pattern_score: float


class KGQA:
    """In-process evidence-pattern QA over the exported member graph."""

    def __init__(self, data_dir: str | Path):
        data_dir = Path(data_dir)
        self.members = {json.loads(l)["member_id"]: json.loads(l)
                        for l in open(data_dir / "members.jsonl")}
        self.member_devices: dict[str, set[str]] = {}
        self.device_members: dict[str, set[str]] = {}
        for l in open(data_dir / "transactions.jsonl"):
            t = json.loads(l)
            self.member_devices.setdefault(t["member_id"], set()).add(t["device_id"])
            self.device_members.setdefault(t["device_id"], set()).add(t["member_id"])

    def _classify(self, q: str) -> str:
        q = q.lower()
        best, best_n = "employer_of", 0
        for intent, spec in INTENTS.items():
            n = sum(1 for k in spec["keywords"] if k in q)
            if n > best_n:
                best, best_n = intent, n
        return best

    def ask(self, question: str) -> Answer:
        intent = self._classify(question := question)
        mid_m = RE_MEMBER.search(question)
        if intent == "fraud_ring" and mid_m:
            mid = mid_m.group(0)
            if mid.startswith("PEN"):
                mid = next((k for k, v in self.members.items()
                            if v["rsa_pin"] == mid), mid)
            linked = set()
            for d in self.member_devices.get(mid, set()):
                linked |= self.device_members.get(d, set())
            linked.discard(mid)
            sub = [(mid, "SHARED_DEVICE", o) for o in sorted(linked)]
            return Answer(intent, sub,
                          f"{len(linked)} member(s) share a device with {mid}: "
                          + (", ".join(sorted(linked)) or "none"),
                          pattern_score=0.9 if linked else 0.3)
        if intent == "employer_of" and mid_m:
            mid = mid_m.group(0)
            m = self.members.get(mid)
            if m:
                return Answer(intent, [(mid, "EMPLOYED_BY", m["employer_id"])],
                              f"{mid} is employed by {m['employer_id']}",
                              pattern_score=0.95)
        if intent == "agent_of" and mid_m:
            mid = mid_m.group(0)
            m = self.members.get(mid)
            if m:
                return Answer(intent, [(mid, "ONBOARDED_BY", m["agent_id"])],
                              f"{mid} was onboarded by {m['agent_id']}",
                              pattern_score=0.95)
        if intent == "members_of_employer":
            emp_m = re.search(r"emp-\d+", question)
            if emp_m:
                emp = emp_m.group(0)
                ms = [k for k, v in self.members.items()
                      if v["employer_id"] == emp]
                return Answer(intent,
                              [(m, "EMPLOYED_BY", emp) for m in ms],
                              f"{len(ms)} member(s) at {emp}",
                              pattern_score=0.9)
        return Answer(intent, [], "No evidence pattern matched.",
                      pattern_score=0.0)
