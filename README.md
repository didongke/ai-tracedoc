# ai-tracedoc

自动记录 Claude Code 开发决策过程：会话结束时提取"你的提问 + AI 最后总结"，
追加进项目根目录的开发过程账本。忠实记录，不推断、不提炼。

## 原理

- SessionEnd hook → hooks/record-session.py → 解析会话转录（JSONL）
- 入账内容：你的每句话原文 + AI 每个回答的最后文字总结
- 不入账：思考过程、工具调用、中间输出、子代理对话
- 原始转录仍留在 `~/.claude/projects/`，本插件不复制、不上传

## 安装（每台机器一次）

本地开发插件实测走 skills-dir 机制（`claude plugin install` 仅支持市场安装）：

    mkdir -p ~/.claude/skills/ai-tracedoc
    cp -r .claude-plugin hooks src ~/.claude/skills/ai-tracedoc/

复制后自动加载，无需手动启用。确认：

    claude plugin list                  # 期望出现 ai-tracedoc@skills-dir，Status ✔ loaded
    claude plugin details ai-tracedoc   # Hooks (1) SessionEnd

（若显示未启用，执行 `claude plugin enable ai-tracedoc`。）

## 启用记录（每个项目一次）

    touch .tracedoc-on

之后该项目所有交互会话在退出时自动记录，无需任何操作。两层开关都打开前不产生任何文件。

## 账本

- 位置：项目根目录，一本持续追加
- 命名：`<创建日期YYYYMMDD>-<项目目录名>-开发过程.md`，如
  `20260906-20260905-ai-tracedoc-开发过程.md`
- 超过 200 KB 自动分卷：续卷名为 `...-开发过程-02.md`、`-03.md`…，
  卷间有"上接/下接"标注；一个会话绝不跨卷

## 建议的 .gitignore

    *开发过程.md
    .tracedoc-state.json
    .tracedoc.lock

账本默认不入库（可能含敏感问答）。分享时按需 `git add` 对应账本文件。

## 注意事项

- headless 模式（`claude -p`）下 SessionEnd 不触发、不会入账；仅交互会话记录
  （2.1.260 实测，已知限制）
- 账本内容是 AI 回答的原文摘录，可能包含代码片段或临时密钥，分享前请检查
- 插件不读取、不记录任何环境变量
- 支持 Linux / macOS；Windows 暂不支持

## 规划

- P2：分析 skill（读账本 + 代码，回答"这里当初为什么这么决定"）
- 其他 agent（Codex 等）：核心层已按跨 agent 设计，待适配
