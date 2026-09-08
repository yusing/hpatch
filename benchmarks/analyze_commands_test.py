"""Command-loop enforcement must not accept damaged evidence as a clean run."""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from analyze_commands import analyze


class CommandEvidenceTests(unittest.TestCase):
    def test_valid_objects_and_blank_lines(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.jsonl"
            event = {"type": "item.completed", "item": {
                "type": "command_execution", "command": "cat example.txt", "exit_code": 0,
            }}
            path.write_text("\n" + json.dumps(event) + "\n\n", encoding="utf-8")
            result = analyze([path])
            self.assertEqual(result["command_execution_items"], 1)
            self.assertEqual(result["categories"]["file_read"]["invocations"], 1)

    def test_malformed_evidence_fails_analyzer_and_enforcement(self):
        root = Path(__file__).resolve().parent
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.jsonl"
            for damaged in (
                "not-json", "[]", "null", '"text"',
                '{"type":"item.completed","value":NaN}',
                '{"type":"item.completed","value":Infinity}',
                '{"type":"item.completed","value":-Infinity}',
                '{"nested":[{"value":NaN}]}',
            ):
                with self.subTest(record=damaged):
                    path.write_text('{}\n' + damaged + '\n', encoding="utf-8")
                    with self.assertRaisesRegex(ValueError, r"events.jsonl:2:"):
                        analyze([path])
                    for command in (
                        [sys.executable, str(root / "analyze_commands.py"), str(path)],
                        ["bash", str(root / "check-edit-loops.sh"), str(root), str(path)],
                    ):
                        result = subprocess.run(command, capture_output=True, text=True, check=False)
                        self.assertNotEqual(result.returncode, 0)
                        self.assertEqual(result.stdout, "")
                        self.assertIn(f"{path}:2:", result.stderr)
                        self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
