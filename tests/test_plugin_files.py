"""Plugin skeleton validation: the manifest and hooks config parse and
carry all required fields."""
import json
import os
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


class TestPluginFiles(unittest.TestCase):
    def test_plugin_json_valid(self):
        path = os.path.join(ROOT, ".claude-plugin", "plugin.json")
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
        self.assertEqual(data["name"], "ai-tracedoc")
        self.assertEqual(data["version"], "0.2.0")
        self.assertIn("development", data["description"])
        self.assertEqual(data["author"], {"name": "ddk"})

    def test_hooks_json_valid(self):
        path = os.path.join(ROOT, "hooks", "hooks.json")
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
        for event in ("SessionEnd", "Stop"):
            self.assertIn(event, data["hooks"])
            hooks = data["hooks"][event]
            self.assertEqual(hooks[0]["matcher"], "*")
            hook_entry = hooks[0]["hooks"][0]
            self.assertEqual(hook_entry["type"], "command")
            self.assertEqual(
                hook_entry["command"],
                'python3 "${CLAUDE_PLUGIN_ROOT}/hooks/record-session.py"',
            )
            self.assertEqual(hook_entry["timeout"], 30)


if __name__ == "__main__":
    unittest.main()
