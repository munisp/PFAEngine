"""KGQA tests — in-process evidence-pattern retrieval."""
import tempfile
import unittest
from pathlib import Path

from pfa_ml import synthgen
from pfa_ml.kgqa import KGQA


class TestKGQA(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = Path(tempfile.mkdtemp())
        synthgen.Generator(seed=5, n_members=200, n_months=6).run(cls.tmp)
        cls.qa = KGQA(cls.tmp)

    def test_employer_lookup(self):
        a = self.qa.ask("who is the employer of mem-000010?")
        self.assertEqual(a.intent, "employer_of")
        self.assertIn("employed by", a.text)

    def test_agent_lookup(self):
        a = self.qa.ask("which agent onboarded mem-000020?")
        self.assertEqual(a.intent, "agent_of")

    def test_members_of_employer(self):
        a = self.qa.ask("how many members at emp-00001?")
        self.assertEqual(a.intent, "members_of_employer")
        self.assertIn("member(s) at emp-00001", a.text)

    def test_fraud_ring(self):
        a = self.qa.ask("who else uses a shared device with mem-000001 (ring check)?")
        self.assertEqual(a.intent, "fraud_ring")

    def test_unmatched(self):
        a = self.qa.ask("what is the weather?")
        self.assertEqual(a.text, "No evidence pattern matched.")


if __name__ == "__main__":
    unittest.main()
