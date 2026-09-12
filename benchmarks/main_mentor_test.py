#!/usr/bin/env python3
"""Model-free checks for diagnostic main mentor capture validation."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from analyze_capture import validate_results


class MainMentorTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.results = Path(self.directory.name) / "results.jsonl"
        self.results.write_text(json.dumps({
            "arm": "mekugi",
            "model": "gpt-5.6-sol",
            "reasoning_effort": "high",
            "agent": {"thread_id": "main", "usage": {
                "input_tokens": 0, "cached_input_tokens": 0,
                "output_tokens": 0, "reasoning_output_tokens": 0,
            }},
        }) + "\n")
        self.config = {
            "benchmark_mode": "mekugi-diagnostic",
            "main_mentor": {
                "enabled": True, "model": "gpt-6-astra",
                "requested_model": "gpt-5.6-sol",
                "requested_reasoning_effort": "high",
            },
        }
        self.metrics = {"exchanges": [
            {"thread_id": "main", "provider_attempts": [{"model": model}]}
            for model in ("gpt-6-astra", "gpt-5.6-sol")
        ]}

    def test_main_schedule(self):
        self.assertEqual(validate_results(self.metrics, self.results, "mekugi", self.config), 1)

    def test_disabled_rejects_mentor_traffic(self):
        self.config["main_mentor"]["enabled"] = False
        with self.assertRaisesRegex(ValueError, "violates the configured schedule"):
            validate_results(self.metrics, self.results, "mekugi", self.config)

    def test_requires_observed_mentor(self):
        self.metrics["exchanges"] = self.metrics["exchanges"][1:]
        with self.assertRaisesRegex(ValueError, "did not start with Astra"):
            validate_results(self.metrics, self.results, "mekugi", self.config)

    def test_invalid_schedule_order(self):
        for models in (
            ("gpt-5.6-sol", "gpt-6-astra"),
            ("gpt-6-astra", "gpt-5.6-sol", "gpt-6-astra"),
        ):
            with self.subTest(models=models):
                self.metrics["exchanges"] = [
                    {"thread_id": "main", "provider_attempts": [{"model": model}]}
                    for model in models
                ]
                with self.assertRaisesRegex(ValueError, "main schedule"):
                    validate_results(self.metrics, self.results, "mekugi", self.config)

    def test_completion_before_handoff(self):
        self.metrics["exchanges"] = self.metrics["exchanges"][:1]
        self.assertEqual(validate_results(self.metrics, self.results, "mekugi", self.config), 1)

    def test_unexpected_model_or_thread(self):
        for field, value in (("model", "gpt-5.6-luna"), ("thread", "unproved")):
            with self.subTest(field=field):
                metrics = copy.deepcopy(self.metrics)
                if field == "model":
                    metrics["exchanges"][0]["provider_attempts"][0]["model"] = value
                else:
                    metrics["exchanges"][0]["thread_id"] = value
                with self.assertRaises(ValueError):
                    validate_results(metrics, self.results, "mekugi", self.config)

    def test_config_must_match_result(self):
        for field, value in (
            ("model", "other"), ("requested_model", "gpt-6-astra"),
            ("requested_reasoning_effort", "low"),
        ):
            with self.subTest(field=field):
                config = copy.deepcopy(self.config)
                config["main_mentor"][field] = value
                with self.assertRaises(ValueError):
                    validate_results(self.metrics, self.results, "mekugi", config)


if __name__ == "__main__":
    unittest.main()
