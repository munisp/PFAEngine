"""End-to-end tests for the ML platform. CPU-only, no network, no PG."""
import json
import tempfile
import unittest
from pathlib import Path

import numpy as np
import torch

from pfa_ml import synthgen
from pfa_ml.features import (batch_encode, encode_tabular, encode_sequence,
                             TABULAR_DIM, SEQ_LEN, SEQ_DIM,
                             feature_version_hash)
from pfa_ml.graph import FraudGraph
from pfa_ml.models.fraud_fusion import FraudFusion
from pfa_ml.models.gnn import GraphSAGEFraud
from pfa_ml.models.mcmc import ThresholdMCMC
from pfa_ml.registry import Registry
from pfa_ml.drift import DriftMonitor, psi, ks_statistic, score_degradation
from pfa_ml.abtest import assign, z_test_two_proportion, ABExperiment
from pfa_ml.train import run_training, load_samples


def _small_dataset(tmp: Path, members: int = 300, months: int = 8) -> Path:
    g = synthgen.Generator(seed=11, n_members=members, n_months=months)
    g.run(tmp)
    return tmp


class TestSynthGen(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.manifest = _small_dataset(self.tmp)

    def test_manifest_counts(self):
        m = json.loads((self.tmp / "manifest.json").read_text())
        self.assertEqual(m["members"], 300)
        self.assertGreater(m["transactions"], 1500)
        self.assertGreater(m["fraud_txns"], 0)

    def test_fraud_archetypes_present(self):
        txns = [json.loads(l) for l in open(self.tmp / "transactions.jsonl")]
        arch = {t["archetype"] for t in txns if t.get("archetype")}
        self.assertTrue({"A1", "A2", "A3", "A4", "A5"} & arch)

    def test_deterministic_seed(self):
        t2 = Path(tempfile.mkdtemp())
        _small_dataset(t2)
        m2 = json.loads((t2 / "manifest.json").read_text())
        m1 = json.loads((self.tmp / "manifest.json").read_text())
        self.assertEqual(m1["dataset_sha256"], m2["dataset_sha256"])

    def test_integer_kobo(self):
        for l in open(self.tmp / "transactions.jsonl"):
            t = json.loads(l)
            self.assertIsInstance(t["amount_kobo"], int)


class TestFeatures(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        _small_dataset(self.tmp, members=50, months=4)

    def test_shapes(self):
        samples = []
        hist = []
        for l in open(self.tmp / "transactions.jsonl"):
            t = json.loads(l)
            samples.append((t, list(hist)))
            hist.append(t)
            if len(samples) >= 100:
                break
        tabs, seqs, labels = batch_encode(samples)
        self.assertEqual(tabs.shape, (100, TABULAR_DIM))
        self.assertEqual(seqs.shape, (100, SEQ_LEN, SEQ_DIM))
        self.assertEqual(labels.shape, (100,))

    def test_determinism(self):
        txn = {"txn_id": "t", "member_id": "m", "kind": "withdrawal",
               "amount_kobo": 50000000, "channel": "ussd", "state": "LA",
               "day": 100, "minute_of_day": 300, "device_id": "d1"}
        a = encode_tabular(txn, [])
        b = encode_tabular(txn, [])
        np.testing.assert_array_equal(a, b)
        self.assertEqual(feature_version_hash(), feature_version_hash())

    def test_outlier_signal(self):
        hist = [{"amount_kobo": 100000, "day": d, "minute_of_day": 600,
                 "device_id": "d1", "state": "LA", "kind": "contribution",
                 "channel": "web", "is_fraud": 0, "txn_id": f"h{d}",
                 "member_id": "m"} for d in range(10)]
        big = {"txn_id": "t", "member_id": "m", "kind": "withdrawal",
               "amount_kobo": 50_000_000, "channel": "ussd", "state": "KN",
               "day": 400, "minute_of_day": 30, "device_id": "dx"}
        v = encode_tabular(big, hist)
        # ratio idx=1 >> baseline, dormancy idx=2 high, novel device idx=4 = 1
        self.assertGreater(v[1], 0.5)
        self.assertGreater(v[2], 0.8)
        self.assertEqual(v[4], 1.0)
        self.assertEqual(v[5], 1.0)


class TestModels(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        _small_dataset(self.tmp, members=120, months=6)

    def test_gnn_forward_and_train(self):
        from pfa_ml.train import train_gnn
        g = FraudGraph.build(self.tmp)
        model, a = train_gnn(g, epochs=3)
        self.assertIsInstance(model, GraphSAGEFraud)
        self.assertGreaterEqual(a, 0.0)
        x = torch.as_tensor(g.node_feat)
        ei = torch.as_tensor(g.edge_index())
        emb = model.embed(x, ei)
        self.assertEqual(emb.shape[1], 32)

    def test_fusion_forward_shapes(self):
        m = FraudFusion()
        tab = torch.randn(4, TABULAR_DIM)
        seq = torch.randn(4, SEQ_LEN, SEQ_DIM)
        emb = torch.randn(4, 32)
        p = m(tab, seq, emb)
        self.assertEqual(p.shape, (4,))
        self.assertTrue(((p >= 0) & (p <= 1)).all())

    def test_mcmc_recovers_cost_optimal_region(self):
        rng = np.random.RandomState(0)
        labels = np.concatenate([np.ones(100), np.zeros(900)]).astype(int)
        probs = np.concatenate([rng.beta(6, 2, 100), rng.beta(2, 8, 900)])
        res = ThresholdMCMC(seed=1).sample(probs, labels, n_samples=800,
                                           burn=200)
        self.assertGreater(res["recall"], 0.5)
        self.assertGreater(res["precision"], 0.5)
        self.assertGreater(res["acceptance_rate"], 0.1)


class TestTrainingEndToEnd(unittest.TestCase):
    def test_full_training_registers_version(self):
        tmp = Path(tempfile.mkdtemp())
        g = synthgen.Generator(seed=3, n_members=600, n_months=10,
                               fraud_rate=0.05)
        g.run(tmp)
        reg_dir = tmp / "reg"
        reg = Registry(root=reg_dir)
        out = run_training(tmp, epochs=3, verbose=False, registry=reg)
        self.assertEqual(out["version"], 1)
        self.assertGreater(out["metrics"]["fusion_val_auc"], 0.6,
                           "fusion must beat chance on synthetic archetypes")
        self.assertGreater(out["metrics"]["recall"], 0.3)
        rec = reg.get("fraudfusion")
        self.assertIsNone(rec)  # registered but not yet promoted
        reg.promote("fraudfusion", 1, "champion")
        champ = reg.get("fraudfusion", "champion")
        self.assertEqual(champ["version"], 1)
        self.assertTrue((Path(champ["path"]) / "fraudfusion.pt").exists())
        self.assertTrue((Path(champ["path"]) / "meta.json").exists())


class TestDriftAndAB(unittest.TestCase):
    def test_psi_zero_for_same_distribution(self):
        rng = np.random.RandomState(0)
        a = rng.normal(0, 1, 2000)
        b = rng.normal(0, 1, 2000)
        self.assertLess(psi(a, b), 0.05)
        self.assertLess(ks_statistic(a, b), 0.1)

    def test_drift_alert_on_shift(self):
        rng = np.random.RandomState(0)
        ref = rng.normal(0, 1, (1000, 3))
        cur = rng.normal(2.5, 1, (1000, 3))
        mon = DriftMonitor(ref)
        res = mon.check(cur)
        self.assertEqual(res["status"], "alert")
        self.assertTrue(res["retrain_recommended"])

    def test_score_degradation(self):
        self.assertTrue(score_degradation(0.95, 0.88)["degraded"])
        self.assertFalse(score_degradation(0.95, 0.94)["degraded"])

    def test_ab_deterministic(self):
        a1 = assign("mem-000001", 10)
        a2 = assign("mem-000001", 10)
        self.assertEqual(a1, a2)
        arms = {assign(f"mem-{i:06d}", 20) for i in range(500)}
        self.assertEqual(arms, {"champion", "challenger"})

    def test_z_test(self):
        res = z_test_two_proportion(60, 1000, 90, 1000)
        self.assertTrue(res["significant"])
        res2 = z_test_two_proportion(60, 1000, 62, 1000)
        self.assertFalse(res2["significant"])

    def test_experiment_promotion_rule(self):
        ex = ABExperiment("t", challenger_pct=50)
        rng = np.random.RandomState(1)
        for i in range(600):
            mid = f"m{i}"
            arm = ex.arm(mid)
            captured = rng.random() < (0.9 if arm == "challenger" else 0.5)
            ex.record(mid, captured)
        rep = ex.report()
        self.assertIn("promote_challenger", rep)


if __name__ == "__main__":
    unittest.main()
