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
            {"sequence": index, "thread_id": "main", "provider_attempts": [{"model": model}]}
            for index, model in enumerate(("gpt-6-astra", "gpt-5.6-sol"), 1)
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
                    {"sequence": index, "thread_id": "main", "provider_attempts": [{"model": model}]}
                    for index, model in enumerate(models, 1)
                ]
                with self.assertRaisesRegex(ValueError, "main schedule"):
                    validate_results(self.metrics, self.results, "mekugi", self.config)

    def test_out_of_order_completion(self):
        self.metrics["exchanges"].reverse()
        self.assertEqual(validate_results(self.metrics, self.results, "mekugi", self.config), 1)

    def test_prewarm_does_not_start_or_end_schedule(self):
        for model in ("gpt-5.6-sol", "gpt-6-astra"):
            with self.subTest(model=model):
                metrics = copy.deepcopy(self.metrics)
                for exchange in metrics["exchanges"]:
                    exchange["sequence"] += 1
                metrics["exchanges"].insert(0, {
                    "sequence": 1, "thread_id": "main",
                    "provider_attempts": [{"model": model}],
                })
                self.assertEqual(validate_results(metrics, self.results, "mekugi", self.config, {1}), 1)
                metrics["exchanges"] = metrics["exchanges"][:1]
                with self.assertRaisesRegex(ValueError, "never routed"):
                    validate_results(metrics, self.results, "mekugi", self.config, {1})

    def test_compaction_does_not_advance_schedule_but_retains_usage(self):
        self.metrics["exchanges"] = [
            {"sequence": index, "thread_id": "main", "request_kind": kind,
             "provider_attempts": [{"model": model}],
             "usage": {"input_tokens": 10, "cached_input_tokens": 0,
                       "output_tokens": 1, "reasoning_tokens": 0}}
            for index, (kind, model) in enumerate((
                ("compaction", "gpt-5.6-sol"),
                ("turn", "gpt-6-astra"),
                ("compaction", "gpt-5.6-sol"),
                ("turn", "gpt-6-astra"),
                ("turn", "gpt-5.6-sol"),
            ), 1)
        ]
        result = json.loads(self.results.read_text())
        result["agent"]["usage"]["input_tokens"] = 50
        result["agent"]["usage"]["output_tokens"] = 5
        self.results.write_text(json.dumps(result) + "\n")
        self.assertEqual(validate_results(self.metrics, self.results, "mekugi", self.config), 1)
        result["agent"]["usage"]["input_tokens"] = 30
        self.results.write_text(json.dumps(result) + "\n")
        with self.assertRaisesRegex(ValueError, "usage differs"):
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
