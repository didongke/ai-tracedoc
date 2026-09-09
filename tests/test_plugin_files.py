"""插件骨架验证：清单与 hooks 配置可解析且字段齐全。"""
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
        self.assertEqual(data["version"], "0.1.0")
        self.assertIn("开发过程", data["description"])
        self.assertEqual(data["author"], {"name": "ddk"})

    def test_hooks_json_valid(self):
        path = os.path.join(ROOT, "hooks", "hooks.json")
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
        self.assertIn("SessionEnd", data["hooks"])
        hooks = data["hooks"]["SessionEnd"]
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
