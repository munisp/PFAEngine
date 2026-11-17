"""Unit tests for the deterministic parts of the kyc-service sidecar.

These cover regex field extraction and date normalisation only — the heavy
models (PaddleOCR, Docling, ONNX runtime) are lazy-loaded and not required.
Run: python -m pytest ml/kyc_service/tests/ -q
"""
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from app import _iso_date, extract_fields_regex  # noqa: E402


class TestIsoDate(unittest.TestCase):
    def test_slash_ddmmyyyy(self):
        self.assertEqual(_iso_date("17/11/2026"), "2026-11-17")

    def test_dash_ddmmyyyy(self):
        self.assertEqual(_iso_date("05-01-1992"), "1992-01-05")

    def test_already_iso(self):
        self.assertEqual(_iso_date("2026-11-17"), "2026-11-17")

    def test_dot_separator(self):
        self.assertEqual(_iso_date("01.12.2030"), "2030-12-01")

    def test_passthrough_garbage(self):
        self.assertEqual(_iso_date("unknown"), "unknown")


class TestExtractFieldsRegex(unittest.TestCase):
    NIN_SLIP = [
        "NATIONAL IDENTITY MANAGEMENT COMMISSION",
        "NIN: 12345678901",
        "AMARA CHINWE OKAFOR",
        "Date of Birth: 05/01/1992",
    ]

    def test_nin_extraction_and_alias(self):
        f = extract_fields_regex(self.NIN_SLIP)
        self.assertEqual(f["document_number"], "12345678901")
        self.assertEqual(f["nin"], "12345678901")

    def test_dob_normalised_to_iso(self):
        f = extract_fields_regex(self.NIN_SLIP)
        self.assertEqual(f["date_of_birth"], "1992-01-05")

    def test_name_heuristic_longest_caps_line(self):
        f = extract_fields_regex(self.NIN_SLIP)
        self.assertEqual(f["full_name"], "Amara Chinwe Okafor")

    def test_expiry(self):
        f = extract_fields_regex(["INTERNATIONAL PASSPORT", "JOHN DOE",
                                  "Date of Expiry 01-12-2030"])
        self.assertEqual(f["expiry_date"], "2030-12-01")

    def test_rc_number(self):
        f = extract_fields_regex(["CERTIFICATE OF INCORPORATION", "RC 1234567"])
        self.assertEqual(f["rc_number"], "RC1234567")

    def test_empty_text_yields_none_fields(self):
        f = extract_fields_regex([])
        self.assertIsNone(f["document_number"])
        self.assertIsNone(f["full_name"])


if __name__ == "__main__":
    unittest.main()
