# ai-tracedoc

自动把 Claude Code 开发对话记录成每个项目一本的 TraceDoc 账本：你的提问 +
AI 的最终回答——忠实记录，不推断、不提炼。

[English](README.md)

## 原理

- Stop hook（每轮 AI 回答完成后）+ SessionEnd hook（会话结束冲洗）
  → bin/tracedoc → 解析会话转录（JSONL）
- 入账内容：你输入的每个问题**原样照录**（中文记中文、英文记英文，不翻译），
  加上 AI 对该问题的最终文字回答
- 不入账：思考过程、工具调用、文件内容、中间输出、子代理对话
- 尚未回答完的提问（AI 正以工具调用收尾）暂缓入账，回答完成后补入
- 原始转录留在 `~/.claude/projects/`，本插件不复制、不上传

## 环境要求

- Claude Code——实测于 2.1.260
- **Linux / macOS / Windows 均可，无需额外安装任何东西**

插件随仓库分发六个平台的预编译二进制（Windows、Linux、macOS，各含 x86-64
与 arm64），因此没有运行时依赖：不需要 Python、不需要 Go、运行时不联网。
Windows 上通过 Git Bash 调用——Claude Code 本身已依赖它。

## 安装（每台机器一次）

通过插件市场安装（推荐）：

```bash
claude plugin marketplace add didongke/ai-tracedoc
claude plugin install ai-tracedoc@ai-tracedoc
```

或手动安装：

```bash
git clone https://github.com/didongke/ai-tracedoc.git
cd ai-tracedoc
mkdir -p ~/.claude/skills/ai-tracedoc
cp -r .claude-plugin hooks bin ~/.claude/skills/ai-tracedoc/
```

手动安装用的是 POSIX shell 命令——Windows 上请在 Git Bash 里执行。
上面的市场安装方式不挑 shell。

确认：

```bash
claude plugin list                  # 期望出现 ai-tracedoc，Status ✔ enabled
claude plugin details ai-tracedoc   # Hooks (2) SessionEnd, Stop
```

## 当前跑的是哪个版本

```
/ai-tracedoc:version
```

输出 Claude Code 实际正在运行的那份副本的版本号。插件可能同时存在于多处——
你正在开发的仓库、市场源、以及 `~/.claude/plugins/cache/` 下每一个装过的版本
——它们互不一致，所以"plugin.json 里的版本号"不是唯一答案。每次安装都落在以
版本号命名的目录里，这意味着升级是新增一个路径，而不是覆盖旧的。

## 启用记录（每个项目一次）

在 Claude Code 里，切到要记录的项目，输入：

```
/ai-tracedoc:on
```

`/ai-tracedoc:off` 关闭，已写下的账本原样保留。记录是**逐项目、默认关闭**的，
没开启过的项目不会产生任何文件。

关闭是**到此为止**，不是暂停：关闭期间说过的内容不会在重新开启时被补录进去，
账本在那里留一段空白。这段空白正是你关掉它的意义。

开关就是项目根目录下一个名为 `.tracedoc-on` 的文件，所以你也可以在 shell 里
自己创建——挑一条适合你的：

```bash
touch .tracedoc-on               # Git Bash / macOS / Linux
type nul > .tracedoc-on          # cmd.exe
New-Item .tracedoc-on            # PowerShell
```

手边有插件的 `bin` 目录的话，`tracedoc --enable` / `tracedoc --disable`
效果相同，且不挑 shell。

之后该项目的问答**逐轮实时入账**（AI 回答完成即记录），会话结束时冲洗收尾，
无需任何操作。两层开关都打开前不产生任何文件。

## 账本

- 位置：项目根目录，一本持续追加
- 命名：`<YYYYMMDD>-<项目目录名>-tracedoc.md`，如
  `20260906-my-project-tracedoc.md`
- 超过 200 KB 自动分卷：续卷名为 `...-tracedoc-02.md`、`-03.md`…，
  卷间有 "Continued in / Continued from" 衔接标注；一个会话绝不跨卷
- 条目格式。空行是格式本身的一部分，不是排版残留——账本是与已落盘文件之间的
  契约，这些空行是刻意复现的，请勿"顺手整理"。

```markdown
## 2026-09-06 · 会话标题

<!-- session: 8f3c1a… -->

**Question:** 2026-09-06 14:32 · 为什么这里这样设计？


**Answer:** ……AI 的最终回答，原样照录……


**Question:** 2026-09-06 14:35 · 一个 AI 从未回答的提问
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
- 记录依赖 Claude Code 的内部转录格式（非公开 API）——Claude Code
  升级后若发现不再入账，请用 `--self-test` 自检（见 Development）
- Windows 上 Smart App Control 可能拒绝未签名的二进制——按文件、间歇性，
  且不提供按应用排除。hook 会点名原因并以非零码退出，不会静默失败。
  记录是**中断而非终止**：下一个能跑起来的 hook 会从同一位置继续，
  除非拒绝持续到整个会话结束，否则不会丢内容。可用
  `Get-WinEvent -LogName Microsoft-Windows-CodeIntegrity/Operational` 确认；
  真正的解法是 Authenticode 签名

## Development

编译发布二进制（六个平台，在任何机器上都能交叉编译）：

```bash
sh build.sh
```

运行测试：

```bash
go test ./...
```

`bin/` 是入库的，所以改完任何 Go 源码都要重跑 `build.sh` 并提交结果。有一个
测试会把入库的二进制与现场编译的版本对比，不一致就失败——否则过期的二进制会
带着一片绿色的测试静默发布出去。

对真实转录离线验证提取管线（不写任何账本）：

```bash
bin/tracedoc --self-test ~/.claude/projects/<项目slug>/<会话ID>.jsonl
```

`internal/tracedoc/` 是与 agent 无关的核心层，`cmd/tracedoc/` 是 Claude Code
适配器，`bin/tracedoc` 是挑选平台二进制的 shell 分发器。仅 `hooks/` 与 `cmd/`
为 Claude Code 专用。

## 写作

四篇系列文章《[AI 思考四部曲](https://dev.to/didongke/series/44232)》（发布于 dev.to）：

1. [AI 不是更聪明的搜索引擎，它是你思想的镜子](https://dev.to/didongke/ai-is-not-a-smarter-search-engine-its-a-mirror-for-your-thinking-bci)
2. [代码可以提交，思考过程归谁？](https://dev.to/didongke/code-can-be-committed-but-who-owns-the-thinking-39k9)
3. [从记录对话到"复活"思想](https://dev.to/didongke/from-recording-conversations-to-resurrecting-thought-ai-is-building-a-digital-pyramid-for-60e)
4. [思想的反射器与文明的加速器](https://dev.to/didongke/the-reflector-of-thought-the-accelerator-of-civilization-how-ai-is-reshaping-the-evolution-of-5293)

*一份为未来起草的宣言——带着可运行的代码。*

## 规划

- 分析 skill：读账本 + 代码，回答"这里当初为什么这么决定"
- 其他 agent（Codex 等）：核心层已按跨 agent 设计，适配器待做
