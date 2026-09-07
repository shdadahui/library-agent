#!/usr/bin/env bash
# 图书馆 Agent —— 项目质量评测脚本
#
# 用法：
#   bash scripts/audit.sh          # 全量评测（含 go test 与 mock agent 评测，较慢）
#   bash scripts/audit.sh --fast   # 快速模式（跳过 go test 与 agent 评测，用近期基线）
#
# 设计原则（与「项目评测集」文档一一对应）：
#   1. 每个维度都必须「可执行」，不依赖主观判断，避免人工打分漂移
#   2. 扣分项必须打印具体证据（文件:行），能直接定位到要改的地方
#   3. 门槛即质量红线：A 级 ≥85，B 级 ≥70，C 级 ≥55，D 级 <55

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FAST=0
[ "${1:-}" = "--fast" ] && FAST=1

export PATH="/g/go/bin:$PATH"
# Go 工具链在 Git Bash 下要求 GOCACHE/GOMODCACHE 为 Windows 风格绝对路径，
# 直接给 "/g/xxx" 会报 "GOMODCACHE entry is relative; must be absolute path"
if command -v cygpath >/dev/null 2>&1; then
  export GOCACHE="$(cygpath -w "$ROOT/.gocache")"
  export GOMODCACHE="$(cygpath -w "$ROOT/.gomod")"
else
  export GOCACHE="$ROOT/.gocache"
  export GOMODCACHE="$ROOT/.gomod"
fi

# ─── 计分器 ────────────────────────────────────────────────────────────
TOTAL=0
ROWS=()
add() { # add <维度> <满分> <得分> <备注>
  local dim="$1" full="$2" got="$3" note="$4"
  TOTAL=$((TOTAL + got))
  ROWS+=("| $dim | $got/$full | $note |")
}

say() { printf '%s\n' "$*"; }

say ""
say "════════════════════════════════════════════════════════════════"
say "  图书馆 Agent —— 项目质量评测"
say "  时间：$(date '+%Y-%m-%d %H:%M:%S')   模式：$([ $FAST -eq 1 ] && echo 快速 || echo 全量)"
say "════════════════════════════════════════════════════════════════"
say ""

# ═══ D1 构建与格式（10 分）══════════════════════════════════════════════
D1=0; D1N=""
if BUILD_ERR=$(go build ./... 2>&1) && [ -z "$BUILD_ERR" ]; then
  D1=$((D1 + 5)); D1N="编译通过"
else
  D1N="❌编译失败"
fi
UNFMT=$(gofmt -l ./cmd ./internal 2>/dev/null | grep -v '^$')
if [ -z "$UNFMT" ]; then
  D1=$((D1 + 5)); D1N="$D1N; gofmt 干净"
else
  D1N="$D1N; ❌未格式化: $(echo "$UNFMT" | tr '\n' ' ')"
fi
add "D1 构建与格式" 10 "$D1" "$D1N"

# ═══ D2 静态分析（10 分）════════════════════════════════════════════════
D2=0; D2N=""
VET=$(go vet ./... 2>&1 | grep -vE 'stat|download| no required module' | grep -v '^$')
if [ -z "$VET" ]; then
  D2=10; D2N="go vet 无告警"
else
  D2=0; D2N="❌ vet: $(echo "$VET" | head -2 | tr '\n' ' ')"
fi
add "D2 静态分析" 10 "$D2" "$D2N"

# ═══ D3 测试覆盖（15 分）════════════════════════════════════════════════
D3=0; D3N=""
if [ $FAST -eq 1 ]; then
  D3=9; D3N="快速模式：沿用基线 ~41%（如需精确请跑全量）"
else
  COV=$(go test ./internal/... -cover -count=1 2>&1 | grep -oE 'coverage: [0-9.]+%' | grep -oE '[0-9.]+')
  if [ -n "$COV" ]; then
    AVG=$(echo "$COV" | awk '{s+=$1; n++} END {printf "%.1f", s/n}')
    D3=$(awk -v a="$AVG" 'BEGIN{if(a>=50)d=15;else if(a>=40)d=12;else if(a>=30)d=9;else if(a>=20)d=6;else d=3;print d}')
    D3N="包均覆盖率 ${AVG}%（≥50→15 / ≥40→12 / ≥30→9）"
  else
    D3N="❌ 无法获取覆盖率"
  fi
fi
add "D3 测试覆盖" 15 "$D3" "$D3N"

# ═══ D4 错误处理：不吞错（10 分）═════════════════════════════════════════
# 吞错 = 用 `_ =` 或 `_, _ =` 丢弃本应处理的 error。曾真实导致故障：
#   静默失败让「通知不生成 / 审计不落库」长期不外显，排查成本极高。
SWALLOW=$(grep -rn "_ = s\.st\.\|_ = st\.\|_, _ = s\.st\.\|_, _ = st\." \
  internal/service internal/store --include='*.go' 2>/dev/null | grep -v _test)
NSW=$(printf '%s' "$SWALLOW" | grep -c . )
D4=$((10 - NSW * 2)); [ $D4 -lt 0 ] && D4=0
if [ "$NSW" -eq 0 ]; then
  D4N="service/store 无吞错"
else
  D4N="❌ 吞错 $NSW 处（首条：$(printf '%s' "$SWALLOW" | head -1 | cut -c1-70)）"
fi
add "D4 错误处理" 10 "$D4" "$D4N"

# ═══ D5 安全（15 分）════════════════════════════════════════════════════
D5=0; D5S=""
grep -q 'func CheckPasswordStrength' internal/auth/auth.go && { D5=$((D5+3)); D5S="领域层密码强度"; }
grep -q 'X-Content-Type-Options' internal/api/api.go && { D5=$((D5+3)); D5S="$D5S; 安全响应头"; }
grep -q 'handleHealth' internal/api/api.go && { D5=$((D5+2)); D5S="$D5S; 健康检查"; }
# SQL 注入：禁止 SELECT/INSERT/UPDATE/DELETE 出现在 fmt.Sprintf 拼接中
SQLCAT=$(grep -rn 'fmt.Sprintf.*\(SELECT\|INSERT\|UPDATE\|DELETE\|WHERE\)' internal/ --include='*.go' 2>/dev/null | grep -v _test)
if [ -z "$SQLCAT" ]; then D5=$((D5+3)); D5S="$D5S; 无 SQL 拼接"; else D5S="$D5S; ❌SQL 拼接 $(printf '%s' "$SQLCAT" | grep -c .) 处"; fi
# 安全测试存在性
if ls internal/api/security_test.go internal/auth/auth_test.go >/dev/null 2>&1; then
  D5=$((D5+4)); D5S="$D5S; 安全测试齐备"
fi
add "D5 安全性" 15 "$D5" "${D5S#; }"

# ═══ D6 Agent 评测通过率（10 分）═════════════════════════════════════════
D6=0; D6N=""
if [ $FAST -eq 1 ]; then
  D6=10; D6N="快速模式：沿用基线 100%（如需精确请跑全量）"
else
  if go build -o bin/eval.exe ./cmd/eval 2>/dev/null; then
    LINE=$(./bin/eval.exe -mock 2>&1 | grep -oE '通过率 [0-9.]+%' | grep -oE '[0-9.]+' | head -1)
    if [ -n "$LINE" ]; then
      D6=$(awk -v r="$LINE" 'BEGIN{printf "%d", r/100*10}')
      D6N="mock 评测通过率 ${LINE}%（按 10 分折算）"
    else
      D6N="❌ 评测未产出通过率"
    fi
  else
    D6N="❌ 评测器编译失败"
  fi
fi
add "D6 Agent 评测" 10 "$D6" "$D6N"

# ═══ D7 依赖与许可证（5 分）══════════════════════════════════════════════
D7=0; D7N=""
DIRECT=$(awk '/^require \(/{f=1;next}/^\)/{f=0}f&&!/indirect/&&NF' go.mod | grep -c .)
if [ "$DIRECT" -le 6 ]; then D7=$((D7+3)); D7N="直接依赖 ${DIRECT} 个（≤6）"; else D7N="直接依赖 ${DIRECT} 个（偏多）"; fi
if [ -f LICENSE ] || [ -f LICENSE.md ]; then D7=$((D7+2)); D7N="$D7N; LICENSE 齐备"; else D7N="$D7N; ❌缺 LICENSE"; fi
add "D7 依赖与许可" 5 "$D7" "$D7N"

# ═══ D8 代码结构（10 分）════════════════════════════════════════════════
# 函数 >100 行意味着职责膨胀，修改时牵一发动全身
LONGFN=$(python3 - <<'PY' 2>/dev/null
import glob
n=0
for f in glob.glob('internal/**/*.go', recursive=True)+glob.glob('cmd/**/*.go', recursive=True):
    if f.endswith('_test.go'): continue
    lines=open(f,encoding='utf-8').read().split(chr(10))
    cur=None;start=0
    for i,l in enumerate(lines):
        if l.startswith('func '):
            if cur and i-start>100: n+=1
            cur=l;start=i
    if cur and len(lines)-start>100: n+=1
print(n)
PY
)
LONGFN=${LONGFN:-0}
if   [ "$LONGFN" -eq 0 ]; then D8=10; elif [ "$LONGFN" -le 2 ]; then D8=8
elif [ "$LONGFN" -le 4 ]; then D8=6; elif [ "$LONGFN" -le 6 ]; then D8=4; else D8=2; fi
add "D8 代码结构" 10 "$D8" ">100 行函数 ${LONGFN} 个（0→10 / ≤2→8 / ≤4→6）"

# ═══ D9 可观测性与韧性（10 分）═══════════════════════════════════════════
D9=0; D9S=""
grep -q 'ReadTimeout' cmd/server/main.go && { D9=$((D9+3)); D9S="HTTP 超时"; }
grep -q 'Shutdown' cmd/server/main.go && { D9=$((D9+3)); D9S="$D9S; 优雅停机"; }
grep -rq 'InsertLoginLog' internal/ --include='*.go' && { D9=$((D9+2)); D9S="$D9S; 登录审计"; }
grep -rq '/api/metrics' internal/api/ --include='*.go' && { D9=$((D9+2)); D9S="$D9S; metrics 端点"; }
add "D9 可观测与韧性" 10 "$D9" "${D9S#; }"

# ═══ D10 文档与 CI（5 分）════════════════════════════════════════════════
D10=0; D10S=""
[ -f README.md ] && { D10=$((D10+1)); D10S="README"; }
[ -d docs ] && [ "$(ls docs | wc -l)" -ge 2 ] && { D10=$((D10+2)); D10S="$D10S; docs $(ls docs | wc -l) 篇"; }
[ -f .github/workflows/ci.yml ] && { D10=$((D10+2)); D10S="$D10S; CI 配置"; }
add "D10 文档与 CI" 5 "$D10" "${D10S#; }"

# ─── 汇总输出 ──────────────────────────────────────────────────────────
say "┌──────────────────────┬───────┬──────────────────────────────────────────────┐"
say "│ 维度                 │ 得分  │ 判定依据                                     │"
say "├──────────────────────┼───────┼──────────────────────────────────────────────┤"
printf '%s\n' "${ROWS[@]}"
say "└──────────────────────┴───────┴──────────────────────────────────────────────┘"
say ""

GRADE="D"
if   [ "$TOTAL" -ge 85 ]; then GRADE="A 🟢";
elif [ "$TOTAL" -ge 70 ]; then GRADE="B 🔵";
elif [ "$TOTAL" -ge 55 ]; then GRADE="C 🟡"; else GRADE="D 🔴"; fi

say "════════════════════════════════════════════════════════════════"
say "  总分：$TOTAL / 100        等级：$GRADE"
say "  门槛：A ≥85   B ≥70   C ≥55   D <55"
say "════════════════════════════════════════════════════════════════"
say ""

# 把分数写入 history 便于跟踪演进
mkdir -p .workbuddy/reports
echo "$(date '+%Y-%m-%d %H:%M')|$TOTAL|$GRADE" >> .workbuddy/reports/audit-history.txt
say "📈 历史趋势：$(tail -5 .workbuddy/reports/audit-history.txt | tr '\n' '  ')"
say ""

exit 0
