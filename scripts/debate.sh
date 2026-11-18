#!/usr/bin/env bash
# 双模型对抗式复核（debate）：
#   提案 → 攻击方证伪 → 防守方逐条处置 → 再攻 → …… 直到放行或达到轮次上限。
#
# 用法：
#   bash scripts/debate.sh <方案文件.md> [最大轮次]
#   ATTACKER_MODEL=claude-sonnet-5-5 DEFENDER_MODEL=grok-4-6 bash scripts/debate.sh docs/x.md 3
#
# 前提：cursor-agent 已登录（agent login）。
# 产物：.cursor/debate/<文件名>.md —— 完整攻防记录，不进 git 也行，但建议留着复盘。
#
# 设计取舍：
#   - 两个模型都不写文件，只输出文本；落盘全部由本脚本完成。
#     这样保证辩论板是唯一事实源，不会被模型顺手改掉。
#   - 用 --mode plan 强制只读，防止任何一方在辩论中途动手改代码。
#   - 每轮上下文靠模型自己 Read 辩论板，而不是把全文塞进 prompt。
#     轮次增加时 prompt 长度不变，只有模型读到的文件在变长。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PROPOSAL="${1:-}"
if [ -z "$PROPOSAL" ] || [ ! -f "$PROPOSAL" ]; then
  echo "用法: bash scripts/debate.sh <方案文件.md> [最大轮次]" >&2
  exit 1
fi

MAX_ROUNDS="${2:-${MAX_ROUNDS:-3}}"
# 防守方 = 出方案的那个模型（主力）,它得为自己的判断辩护并给依据。
# 攻击方 = 另一个谱系的模型,负责证伪。异质比强弱更重要。
ATTACKER_MODEL="${ATTACKER_MODEL:-composer-2-5}"
DEFENDER_MODEL="${DEFENDER_MODEL:-grok-4-6}"
BOARD_DIR="$ROOT/.cursor/debate"
SLUG="$(basename "$PROPOSAL" .md)"
BOARD="$BOARD_DIR/${SLUG}.md"

command -v cursor-agent >/dev/null 2>&1 || {
  echo "错误：找不到 cursor-agent。安装：curl https://cursor.com/install -fsS | bash" >&2
  exit 1
}

if ! cursor-agent --list-models >/dev/null 2>&1; then
  echo "错误：cursor-agent 未登录。请先在终端运行：agent login" >&2
  exit 1
fi

mkdir -p "$BOARD_DIR"

{
  echo "# 对抗式复核 · ${SLUG}"
  echo
  echo "- 攻击方：${ATTACKER_MODEL}"
  echo "- 防守方：${DEFENDER_MODEL}"
  echo "- 轮次上限：${MAX_ROUNDS}"
  echo "- 提案文件：${PROPOSAL}"
  echo
  echo "---"
  echo
  echo "## 原始提案"
  echo
  cat "$PROPOSAL"
} > "$BOARD"

echo "辩论板：$BOARD"

read -r -d '' ATTACK_PROMPT <<EOF
你在这场对抗式复核里担任**攻击方**。

先用 Read 工具读这个文件，它包含原始提案和已有的全部攻防记录：
${BOARD}

你的任务不是"复核"，是**证伪**：假设这份提案注定要失败，找出它失败的具体方式。

规则：

1. 每条问题必须给出具体失败场景——什么输入、什么环境、什么并发时序、什么下游用法会让它出错。给不出场景的猜想不要写。
2. 已被防守方 ACCEPT 且给出了修正方案的点，不许重复提。
3. 上一轮被 REJECT 的点，如果你认为防守方的依据不成立，可以再攻一次，但必须直接反驳那个依据本身，不能只是重复原判断。
4. 不要为了保持对抗姿态而编造问题。如果确实攻不动，就放行。

输出格式，每条一行：
[BLOCK|MAJOR|MINOR] 位置 — 失败场景 — 为什么这会导致问题

严重度：
- BLOCK：照做会导致编译失败、破坏下游、丢数据、线上事故
- MAJOR：能跑，但特定条件下出问题
- MINOR：可以更好，不影响正确性

最后一行必须是二者之一：
VERDICT: PASS
VERDICT: BLOCK
EOF

read -r -d '' DEFEND_PROMPT <<EOF
你在这场对抗式复核里担任**防守方**。

先用 Read 工具读这个文件，重点看最后一节攻击方的意见：
${BOARD}

对攻击方提出的**每一条**，独立给出处置。只能选三种：

- ACCEPT — 承认这是真问题，并给出具体修正：改哪里、改成什么
- REJECT — 驳回，必须给出**可验证依据**：文档出处、代码位置、可运行的反例
- PARTIAL — 部分成立，说明成立和不成立的边界在哪

强制要求：

1. 逐条编号回应，禁止把多条合并成一条。
2. 禁止因为对方语气强硬就 ACCEPT。只有当你确认它描述的事实成立时，才算成立。
3. 禁止用"综合考虑""权衡之后仍然保留""这点值得注意"这类含糊表述回避判断。
4. 如果攻击方给的失败场景在真实使用中不会出现，就是 REJECT，并说明为什么不会出现。
5. 你不需要赢。你需要的是一份经得起复查的处置记录。

输出格式，每条一行：
#<编号> ACCEPT|REJECT|PARTIAL — 理由（REJECT 必须带依据）

最后一行：
ROUND_SUMMARY: 还剩 N 个未解决分歧
EOF

final_verdict="未收敛"

for round in $(seq 1 "$MAX_ROUNDS"); do
  echo ""
  echo "==> 第 ${round} 轮 · 攻击方（${ATTACKER_MODEL}）"

  if ! attack="$(cursor-agent -p --mode plan --model "$ATTACKER_MODEL" "$ATTACK_PROMPT" 2>/dev/null)"; then
    echo "攻击方调用失败，已中止。辩论板保留到当前状态。" >&2
    exit 2
  fi
  attack="${attack:-（无输出）}"

  {
    echo
    echo "## 第 ${round} 轮 · 攻击方（${ATTACKER_MODEL}）"
    echo
    echo "$attack"
  } >> "$BOARD"

  if printf '%s' "$attack" | grep -q 'VERDICT: PASS'; then
    final_verdict="第 ${round} 轮攻击方放行"
    echo "    放行，辩论结束"
    break
  fi

  echo "==> 第 ${round} 轮 · 防守方（${DEFENDER_MODEL}）"

  if ! defend="$(cursor-agent -p --mode plan --model "$DEFENDER_MODEL" "$DEFEND_PROMPT" 2>/dev/null)"; then
    echo "防守方调用失败，已中止。辩论板保留到当前状态。" >&2
    exit 2
  fi
  defend="${defend:-（无输出）}"

  {
    echo
    echo "## 第 ${round} 轮 · 防守方（${DEFENDER_MODEL}）"
    echo
    echo "$defend"
  } >> "$BOARD"

  if [ "$round" -eq "$MAX_ROUNDS" ]; then
    final_verdict="达到轮次上限 ${MAX_ROUNDS}，仍有分歧"
  fi
done

{
  echo
  echo "---"
  echo
  echo "## 结果"
  echo
  echo "${final_verdict}"
} >> "$BOARD"

echo ""
echo "==> 结束：${final_verdict}"
echo "==> 完整攻防记录：${BOARD}"
echo ""
echo "下一步：读 ACCEPT 项照改；REJECT 项由你自己裁决，不要默认接受任何一方。"

if printf '%s' "$final_verdict" | grep -q '仍有分歧'; then
  exit 3
fi
