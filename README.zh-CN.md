# ai-tracedoc

自动把 Claude Code 开发对话记录成每个项目一本的 TraceDoc 账本：你的提问 +
AI 的最终回答——忠实记录，不推断、不提炼。

[English](README.md)

## 原理

- Stop hook（每轮 AI 回答完成后）+ SessionEnd hook（会话结束冲洗）
  → hooks/record-session.py → 解析会话转录（JSONL）
- 入账内容：你输入的每个问题**原样照录**（中文记中文、英文记英文，不翻译），
  加上 AI 对该问题的最终文字回答
- 不入账：思考过程、工具调用、文件内容、中间输出、子代理对话
- 尚未回答完的提问（AI 正以工具调用收尾）暂缓入账，回答完成后补入
- 原始转录留在 `~/.claude/projects/`，本插件不复制、不上传

## 环境要求

- Claude Code——仅在 **Linux** 上实测（2.1.260）
- macOS 理论兼容（POSIX `fcntl`），未实测
- Windows 不支持

## 安装（每台机器一次）

```bash
mkdir -p ~/.claude/skills/ai-tracedoc
cp -r .claude-plugin hooks src ~/.claude/skills/ai-tracedoc/
```

复制后自动加载。确认：

```bash
claude plugin list                  # 期望出现 ai-tracedoc@skills-dir，Status ✔ loaded
claude plugin details ai-tracedoc   # Hooks (2) SessionEnd, Stop
```

## 启用记录（每个项目一次）

```bash
touch .tracedoc-on
```

之后该项目的问答**逐轮实时入账**（AI 回答完成即记录），会话结束时冲洗收尾，
无需任何操作。两层开关都打开前不产生任何文件。

## 账本

- 位置：项目根目录，一本持续追加
- 命名：`<YYYYMMDD>-<项目目录名>-tracedoc.md`，如
  `20260906-my-project-tracedoc.md`
- 超过 200 KB 自动分卷：续卷名为 `...-tracedoc-02.md`、`-03.md`…，
  卷间有 "Continued in / Continued from" 衔接标注；一个会话绝不跨卷
- 条目格式：

```markdown
## 2026-09-06 · 会话标题

**Question:** 2026-09-06 14:32 · 为什么这里这样设计？
**Answer:** ……AI 的最终回答，原样照录……
```

## 建议的 .gitignore

```gitignore
*tracedoc.md
.tracedoc-state.json
.tracedoc.lock
```

账本默认不入库（可能含敏感问答）。分享时按需 `git add` 对应账本文件。

## 注意事项

- 问答在会话进行中就已入账，退出方式（`/exit`、Ctrl+D、关闭终端、进程强杀）
  都不影响已完成内容的记录；最多可能缺"AI 尚未回答完的最后一个提问"
- headless（`claude -p`）会话同样逐轮入账（Stop hook 触发）
- 账本内容是 AI 回答的原文摘录，可能包含代码片段或临时密钥，分享前请检查
- 插件不读取、不记录任何环境变量

## 规划

- 分析 skill：读账本 + 代码，回答"这里当初为什么这么决定"
- 其他 agent（Codex 等）：核心层已按跨 agent 设计，适配器待做
