#!/usr/bin/env bash
#
# 序流 —— 核心闭环 e2e 脚本
#
# 它打的是**真实 HTTP API**（不是单测里的 httptest），走完一整条业务链路：
#
#   登录 → 建产品 → 建模块 → 提需求 → 提交评审 → 评审通过
#   → 建项目 + 迭代 + 加成员 → 需求排进迭代 → 拆任务 → 开始 → 记工时 → 完成
#   → 报缺陷 → 指派 → 解决 → 关闭 → 重新激活
#   → 查操作历史 → 校验 progress 与工时公式一致 → 看工作台/通知/燃尽图
#
# 之所以要单测之外再有一份，是因为单测每个包各自装配依赖，谁也证明不了
# 「路由、中间件、权限、事务、序列化合起来还能跑」——而用户用的正是后者。
#
# 用法：
#   bash scripts/e2e.sh
#       自起一个临时服务（sqlite 临时库 + 随机端口），跑完关掉。
#       这是默认行为：一条命令就是一次完整的回归。
#
#   XULIU_E2E_SKIP_BOOT=1 bash scripts/e2e.sh
#       对着已经在跑的服务跑（默认 http://127.0.0.1:8080，可用 XULIU_E2E_BASE 指定）。
#
#   XULIU_E2E_DRIVER=mysql XULIU_E2E_DSN='xuliu:pwd@tcp(127.0.0.1:3306)/xuliu?...' bash scripts/e2e.sh
#       换库回归之一：脚本自己起服务，但连到外部那个库。要求服务端已经在跑
#       （没有服务端会在启动阶段就失败，并把服务日志打出来）。
#
#   XULIU_E2E_SKIP_BOOT=1 XULIU_E2E_BASE=http://127.0.0.1:8081 bash scripts/e2e.sh
#       换库回归之二：打 docker compose 起好的那一整套（mysql 在 :8081、postgres 在 :8082）。
#       注意只给 XULIU_E2E_BASE 而不给 SKIP_BOOT 会被拒绝 —— 自起模式会覆盖 BASE，
#       那样跑完是 SQLite 的绿灯，不是换库的绿灯。
#
# 依赖：curl + （jq 或 node 或 python3 任一，用来取字段）。全程只用这些。

set -uo pipefail

# 脚本假定在仓库根目录运行（要 go build ./cmd/xuliu、要读 configs/）。
# 从别处调用时自己找回去，省得每次都先 cd。
if [ ! -f go.mod ] && [ -f "$(dirname "$0")/../go.mod" ]; then
	cd "$(dirname "$0")/.." || exit 2
fi

# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------

BASE="${XULIU_E2E_BASE:-http://127.0.0.1:8080}"
API="$BASE/api/v1"

PORT="${XULIU_E2E_PORT:-18081}"
SKIP_BOOT="${XULIU_E2E_SKIP_BOOT:-0}"
DRIVER="${XULIU_E2E_DRIVER:-}"
DSN="${XULIU_E2E_DSN:-}"

# 管理员账号：自起服务时同时是 bootstrap 的初始账号。
ACCOUNT="${XULIU_E2E_ACCOUNT:-admin}"
PASSWORD="${XULIU_E2E_PASSWORD:-admin123456}"

# XULIU_E2E_BASE 说的是「打哪台已有服务」，而自起模式会在 start_server 里把
# BASE 改成临时服务自己的地址 —— 两个都给时前者被静默覆盖。
# 于是「以为在打 :8081 上那个 MySQL 实例，实际打的是刚起来的 SQLite」，
# 123 项全绿而换库一次没验过。宁可拒绝这一组组合，也不要给出假的绿灯。
if [ -n "${XULIU_E2E_BASE:-}" ] && [ "$SKIP_BOOT" != "1" ]; then
	echo "XULIU_E2E_BASE 指向已有服务，必须同时给 XULIU_E2E_SKIP_BOOT=1，" >&2
	echo "否则脚本会自起一个临时服务并覆盖该地址（换库回归就变成白跑 SQLite）。" >&2
	echo "正确写法：XULIU_E2E_SKIP_BOOT=1 XULIU_E2E_BASE=http://127.0.0.1:8081 bash scripts/e2e.sh" >&2
	exit 2
fi

# 口令强度检查会拒绝「常见口令」，所以 e2e 用户用一个不含常见词的短语。
DEV_ACCOUNT="e2e_dev"
DEV_PASSWORD="Xq7#mango-leaf"
DEV_REALNAME="e2e 开发"
TESTER_ACCOUNT="e2e_qa"
TESTER_PASSWORD="Xq7#mango-leaf"
TESTER_REALNAME="e2e 测试"

# 工时常量：进度公式的输入，最后会拿它反算期望值。
TASK_ESTIMATE=8
EFFORT_CONSUMED=5
EFFORT_LEFT=3

# 报工日期与燃尽图区间都用「今天」推导 —— 任务是在本次运行里建的，
# 燃尽图只统计任务创建之后的日期，取死日期会让曲线凭空变空。
#
# 一律用 UTC：服务端的工时与燃尽图按 UTC 日聚合（时间口径全平台统一 UTC，
# 见 configs/config.yaml）。用本机本地日期的话，东八区晚上跑这条脚本会拿到
# 「客户端说是明天、服务端记成今天」，同一天的工时被切进两个日期桶，
# 于是「列表按日期倒序」看起来跳了 —— 那天的差一天，不是排序的错。
today() { date -u +%F; }
days_from_today() { # days_from_today <整数天数>
	date -u -d "$1 day" +%F 2>/dev/null || date -u -v"$1"d +%F
}
TODAY="$(today)"
BURN_BEGIN="$(days_from_today -3)"
BURN_END="$(days_from_today +3)"

# ---------------------------------------------------------------------------
# 输出
# ---------------------------------------------------------------------------

if [ -t 1 ]; then
	CLR_OK=$'\033[32m'; CLR_BAD=$'\033[31m'; CLR_DIM=$'\033[2m'; CLR_RST=$'\033[0m'
else
	CLR_OK=; CLR_BAD=; CLR_DIM=; CLR_RST=
fi

PASSED=0
FAILED=0

step() { printf '\n%s== %s ==%s\n' "$CLR_DIM" "$1" "$CLR_RST"; }
ok()   { PASSED=$((PASSED + 1)); printf '  %s[ok]%s   %s\n' "$CLR_OK" "$CLR_RST" "$1"; }
bad()  { FAILED=$((FAILED + 1)); printf '  %s[FAIL]%s %s\n' "$CLR_BAD" "$CLR_RST" "$1"; }

check_eq() { # check_eq 说明 实际 期望
	if [ "$2" = "$3" ]; then ok "$1 = $2"; else bad "$1 期望 [$3]，实际 [$2]"; fi
}

check_num() { # 数值比较：容忍 5 与 5.0 的写法差异
	local a="$2" b="$3"
	if awk -v x="$a" -v y="$b" 'BEGIN{ d = x - y; exit !(d < 0.001 && d > -0.001) }' 2>/dev/null; then
		ok "$1 = $2"
	else
		bad "$1 期望 $3，实际 [$2]"
	fi
}

check_has() { # check_has 说明 大字符串 子串
	case "$2" in
		*"$3"*) ok "$1 含 $3" ;;
		*) bad "$1 缺 $3（实际：$2）" ;;
	esac
}

check_nonempty() {
	if [ -n "$2" ] && [ "$2" != "null" ]; then ok "$1 = $2"; else bad "$1 为空"; fi
}

# ---------------------------------------------------------------------------
# 运行环境：临时目录、JSON 取值器、临时服务
# ---------------------------------------------------------------------------

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t xuliu-e2e)"
# 传给被启动的那个原生二进制时得用 Windows 路径（git-bash 的 /tmp 它看不懂）。
# shell 自己用的路径仍走 $TMP。/ 混用无妨，Go 认。
BIN_TMP="$TMP"
if command -v cygpath >/dev/null 2>&1; then
	BIN_TMP="$(cygpath -w "$TMP" 2>/dev/null || printf '%s' "$TMP")"
fi
RESP="$TMP/resp.json"
REQ="$TMP/req.json"
SERVER_PID=""
SERVER_LOG="$TMP/server.log"

cleanup() {
	if [ -n "$SERVER_PID" ]; then
		kill "$SERVER_PID" 2>/dev/null
		wait "$SERVER_PID" 2>/dev/null
	fi
}
trap cleanup EXIT

# 选一个 JSON 取值器。三者取其一即可，不强制装 jq。
JSON_TOOL=""
if command -v jq >/dev/null 2>&1; then
	JSON_TOOL=jq
elif command -v node >/dev/null 2>&1; then
	JSON_TOOL=node
elif command -v python >/dev/null 2>&1; then
	JSON_TOOL=python
fi

if [ -z "$JSON_TOOL" ]; then
	echo "需要 jq、node 或 python 之一来解析 JSON 响应" >&2
	exit 2
fi

# json_get '<json>' 'data.list.0.id' —— 路径里的数字段按数组下标解释。
json_get() {
	local json="$1" path="$2"
	case "$JSON_TOOL" in
	jq)
		printf '%s' "$json" | jq -r --arg p "$path" \
			'getpath($p | split(".") | map(if test("^[0-9]+$") then tonumber else . end)) | if . == null then "" else tostring end' 2>/dev/null
		;;
	node)
		printf '%s' "$json" | node -e 'const fs=require("fs");let d=JSON.parse(fs.readFileSync(0,"utf8")||"null");for(const k of process.argv[1].split(".")){d=(d==null)?null:(/^[0-9]+$/.test(k)?d[+k]:d[k]);}process.stdout.write(d==null?"":String(d));' "$path" 2>/dev/null
		;;
	python)
		printf '%s' "$json" | python -c '
import json,sys
d = json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d = d[int(k)] if isinstance(d, list) else d[k]
sys.stdout.write("" if d is None else str(d))
' "$path" 2>/dev/null
		;;
	esac
}

# json_dump '<json>' 'data.list' —— 取出某一段并压成紧凑 JSON 文本。
#
# 与 json_get 的分工：json_get 取的是「一个值」（拿来做相等断言），
# json_dump 取的是「一段结构」（拿来做包含断言）。对象数组直接拼进
# 字符串会变成 [object Object]，所以这里必须走序列化。
json_dump() {
	local json="$1" path="$2"
	case "$JSON_TOOL" in
	jq)
		printf '%s' "$json" | jq -c --arg p "$path" \
			'getpath($p | split(".") | map(if test("^[0-9]+$") then tonumber else . end))' 2>/dev/null
		;;
	node)
		printf '%s' "$json" | node -e 'const fs=require("fs");let d=JSON.parse(fs.readFileSync(0,"utf8")||"null");for(const k of process.argv[1].split(".")){d=(d==null)?null:(/^[0-9]+$/.test(k)?d[+k]:d[k]);}process.stdout.write(JSON.stringify(d==null?null:d));' "$path" 2>/dev/null
		;;
	python)
		printf '%s' "$json" | python -c '
import json,sys
d = json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d = d[int(k)] if isinstance(d, list) else d[k]
sys.stdout.write(json.dumps(d, ensure_ascii=False))
' "$path" 2>/dev/null
		;;
	esac
}

# get <path> —— 从上一次响应里取值，让调用处读起来像在读响应体。
get() { json_get "$HTTP_BODY" "$1"; }

# dump <path> —— 从上一次响应里取一段结构（用于包含断言）。
dump() { json_dump "$HTTP_BODY" "$1"; }

# json_lint <文件> —— 校验文件内容是不是合法 JSON。
json_lint() {
	case "$JSON_TOOL" in
	jq) jq -e . "$1" >/dev/null 2>&1 ;;
	node) node -e 'JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"))' "$1" >/dev/null 2>&1 ;;
	python) python -c 'import json,sys; json.load(open(sys.argv[1]))' "$1" >/dev/null 2>&1 ;;
	*) return 0 ;;
	esac
}

# start_server 起一个临时服务。
#
# 用 go build 而不是 go run：go run 会多起一层父进程，
# 在 Windows 上杀掉它不一定能带走真正监听端口的子进程。
start_server() {
	if ! command -v go >/dev/null 2>&1; then
		echo "未找到 go，无法自起服务。请先启动服务再以 XULIU_E2E_SKIP_BOOT=1 运行。" >&2
		exit 2
	fi

	printf '  构建 xuliu …\n'
	if ! go build -o "$TMP/xuliu.exe" ./cmd/xuliu 2>"$TMP/build.log"; then
		echo "构建失败：" >&2
		cat "$TMP/build.log" >&2
		exit 1
	fi

	BASE="http://127.0.0.1:$PORT"
	API="$BASE/api/v1"

	# 环境变量覆盖配置：临时库、临时附件目录、指定端口、固定初始口令。
	# 前缀 XULIU_ + 双下划线表达层级（见 internal/config）。
	local env_driver=()
	[ -n "$DRIVER" ] && env_driver=(XULIU_DATABASE__DRIVER="$DRIVER")

	env "${env_driver[@]}" \
		XULIU_APP__ENV=test \
		XULIU_SERVER__ADDR=":$PORT" \
		XULIU_DATABASE__DSN="${DSN:-$BIN_TMP/e2e.db}" \
		XULIU_STORAGE__LOCAL__DIR="$BIN_TMP/uploads" \
		XULIU_APP__BOOTSTRAP__ACCOUNT="$ACCOUNT" \
		XULIU_APP__BOOTSTRAP__PASSWORD="$PASSWORD" \
		"$TMP/xuliu.exe" -config configs/config.yaml >"$SERVER_LOG" 2>&1 &
	SERVER_PID=$!

	# 轮询 /healthz，最多等 60 秒（首次启动要建表 + 种子）。
	local waited=0
	while [ "$waited" -lt 60 ]; do
		if curl -fsS --max-time 2 "$BASE/healthz" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$SERVER_PID" 2>/dev/null; then
			echo "服务启动即退出，日志：" >&2
			cat "$SERVER_LOG" >&2
			exit 1
		fi
		sleep 1
		waited=$((waited + 1))
	done
	echo "等待 /healthz 超时，日志：" >&2
	cat "$SERVER_LOG" >&2
	exit 1
}

# ---------------------------------------------------------------------------
# HTTP
# ---------------------------------------------------------------------------

TOKEN=""

# call <说明> <方法> <路径> [请求体文件]
#
# 任何非 2xx 都算失败并打印请求与响应 —— 报错时最想看的就是这两样。
call() {
	local label="$1" method="$2" path="$3" body="${4-}"

	# 发出去之前先确认请求体是合法 JSON。
	# 拼 JSON 的写法是 shell 里最容易错的一处（少一个引号就会静默拼出个畸形对象，
	# 服务端只会回一句「请求格式不正确」，让人去查业务逻辑而不是查脚本），
	# 所以在最靠近机器的地方挡住它。
	if [ -n "$body" ] && ! json_lint "$body"; then
		bad "$label：请求体不是合法 JSON（脚本拼错了）"
		printf '         %s%s%s\n' "$CLR_DIM" "$(cat "$body")" "$CLR_RST"
		return 1
	fi

	local args=(-sS --max-time 30 -o "$RESP" -w '%{http_code}' -X "$method" "$API$path" -H 'Accept: application/json')
	[ -n "$TOKEN" ] && args+=(-H "Authorization: Bearer $TOKEN")
	if [ -n "$body" ]; then
		args+=(-H 'Content-Type: application/json' --data-binary "@$body")
	fi

	local code
	if ! code="$(curl "${args[@]}")"; then
		bad "$label：请求失败（服务没起来？）"
		return 1
	fi
	HTTP_CODE="$code"
	HTTP_BODY="$(cat "$RESP" 2>/dev/null)"

	case "$code" in
	2*)
		ok "$label"
		return 0
		;;
	*)
		bad "$label：HTTP $code $(get message)"
		printf '         %s→ %s %s%s\n' "$CLR_DIM" "$method" "$API$path" "$CLR_RST"
		[ -n "$body" ] && printf '         %s载荷 %s%s\n' "$CLR_DIM" "$(cat "$body")" "$CLR_RST"
		printf '         %s响应 %s%s\n' "$CLR_DIM" "$(printf '%s' "$HTTP_BODY" | head -c 500)" "$CLR_RST"
		return 1
		;;
	esac
}

# 直接打一个 URL（用于带查询串的 GET，避免在 call 里再拼一次）。
call_url() {
	local label="$1" url="$2"
	local code
	if ! code="$(curl -sS --max-time 30 -o "$RESP" -w '%{http_code}' "$url" \
		-H 'Accept: application/json' -H "Authorization: Bearer $TOKEN")"; then
		bad "$label：请求失败"
		return 1
	fi
	HTTP_CODE="$code"
	HTTP_BODY="$(cat "$RESP" 2>/dev/null)"
	case "$code" in
	2*) ok "$label"; return 0 ;;
	*) bad "$label：HTTP $code $(get message)"; return 1 ;;
	esac
}

# call_status <说明> <方法> <路径或整条 URL> <期望状态码> [请求体文件]
#
# 用于「非 2xx 才是正确结果」的断言：越权访问、状态机守卫拒绝，
# 这些情况下 4xx 正是我们要的结果，不该被记成失败。
call_status() {
	local label="$1" method="$2" target="$3" want="$4" body="${5-}"
	local url="$target"
	case "$target" in http*) ;; *) url="$API$target" ;; esac

	local args=(-sS --max-time 30 -o "$RESP" -w '%{http_code}' -X "$method" "$url" -H 'Accept: application/json')
	[ -n "$TOKEN" ] && args+=(-H "Authorization: Bearer $TOKEN")
	[ -n "$body" ] && args+=(-H 'Content-Type: application/json' --data-binary "@$body")

	local code
	if ! code="$(curl "${args[@]}")"; then
		bad "$label：请求失败"
		return 1
	fi
	HTTP_CODE="$code"
	HTTP_BODY="$(cat "$RESP" 2>/dev/null)"
	check_eq "$label（HTTP $code）" "$code" "$want"
}

# body_of '关键字段:值' … —— 把若干 "键":值 片段拼成一个 JSON 对象。
#
# 每个参数都是**一整段**已经写好的 "键":值（引号自己带），
# 这样调用处不必再为字符串/数字/数组各写一个格式串。
body_of() {
	printf '{%s}' "$(printf '%s,' "$@" | sed 's/,$//')"
}

# ---------------------------------------------------------------------------
# 开跑
# ---------------------------------------------------------------------------

printf '\n序流 e2e —— %s\n' "$API"

if [ "$SKIP_BOOT" = "1" ]; then
	printf '  复用已有服务（XULIU_E2E_SKIP_BOOT=1）\n'
else
	start_server
fi

printf '  JSON 取值器：%s\n' "$JSON_TOOL"

# ---------------------------------------------------------------------------
step "0. 健康检查"
# ---------------------------------------------------------------------------

if curl -fsS --max-time 5 "$BASE/healthz" >/dev/null 2>&1; then
	ok "GET /healthz 200"
else
	bad "GET /healthz 不通（服务没起来？）"
	printf '\n服务不可达，后续步骤无法继续。\n'
	exit 1
fi

# ---------------------------------------------------------------------------
step "1. 登录"
# ---------------------------------------------------------------------------

body_of '"account":"'"$ACCOUNT"'"' '"password":"'"$PASSWORD"'"' >"$REQ"
call "管理员登录" POST /auth/login "$REQ" || {
	printf '\n登录失败。若对着已有服务跑，请用 XULIU_E2E_ACCOUNT / XULIU_E2E_PASSWORD 指定账号。\n'
	exit 1
}
ADMIN_TOKEN="$(get data.accessToken)"
ADMIN_ID="$(get data.profile.id)"
check_nonempty "拿到 accessToken" "$ADMIN_TOKEN"
check_nonempty "管理员 id" "$ADMIN_ID"

TOKEN="$ADMIN_TOKEN"
call_url "读取个人资料" "$API/auth/profile" && check_eq "超管标记 isSuper" "$(get data.isSuper)" "true"

# ---------------------------------------------------------------------------
step "2. 建两个用户：研发 + 测试"
# ---------------------------------------------------------------------------

# 权限组按名字查，不写死 id —— 种子顺序变了脚本不该跟着坏。
call_url "查权限组（研发）" "$API/groups?keywords=%E7%A0%94%E5%8F%91" || exit 1
DEV_GROUP="$(get data.list.0.id)"
call_url "查权限组（测试）" "$API/groups?keywords=%E6%B5%8B%E8%AF%95" || exit 1
TESTER_GROUP="$(get data.list.0.id)"
check_nonempty "研发组 id" "$DEV_GROUP"
check_nonempty "测试组 id" "$TESTER_GROUP"

body_of \
	'"account":"'"$DEV_ACCOUNT"'"' \
	'"password":"'"$DEV_PASSWORD"'"' \
	'"realname":"'"$DEV_REALNAME"'"' \
	'"jobRole":"dev"' \
	'"groups":['"$DEV_GROUP"']' >"$REQ"
call "建用户 $DEV_ACCOUNT" POST /users "$REQ" || exit 1
DEV_ID="$(get data.id)"

body_of \
	'"account":"'"$TESTER_ACCOUNT"'"' \
	'"password":"'"$TESTER_PASSWORD"'"' \
	'"realname":"'"$TESTER_REALNAME"'"' \
	'"jobRole":"qa"' \
	'"groups":['"$TESTER_GROUP"']' >"$REQ"
call "建用户 $TESTER_ACCOUNT" POST /users "$REQ" || exit 1
TESTER_ID="$(get data.id)"

# 两个用户各自登录一次，后面要用他们的身份做动作（权限与可见性都是按人算的）。
login_as() { # login_as <账号> <口令> -> 打印 token
	body_of '"account":"'"$1"'"' '"password":"'"$2"'"' >"$REQ"
	local saved="$TOKEN"
	TOKEN=""
	# 日志走 stderr：这个函数的 stdout 要留给 token，混进一行 [ok] 就废了。
	call "$1 登录" POST /auth/login "$REQ" >&2 || return 1
	TOKEN="$saved"
	get data.accessToken
}

DEV_TOKEN="$(login_as "$DEV_ACCOUNT" "$DEV_PASSWORD")"
TESTER_TOKEN="$(login_as "$TESTER_ACCOUNT" "$TESTER_PASSWORD")"
check_nonempty "研发 token" "$DEV_TOKEN"
check_nonempty "测试 token" "$TESTER_TOKEN"

# ---------------------------------------------------------------------------
step "3. 建产品与模块"
# ---------------------------------------------------------------------------

body_of \
	'"name":"e2e 产品"' \
	'"code":"e2eprd"' \
	'"po":'"$ADMIN_ID" \
	'"reviewStory":true' \
	'"reviewer":['"$ADMIN_ID"']' >"$REQ"
call "建产品" POST /products "$REQ" || exit 1
PRODUCT_ID="$(get data.id)"
check_nonempty "产品 id" "$PRODUCT_ID"

# 模块树按用途各建一棵：需求挂需求树、任务挂任务树。
# 跨树挂载会被拒（module_wrong_root）—— 一棵树下的排序与层级是给同一类对象用的。
body_of '"root":'"$PRODUCT_ID" '"type":"story"' '"name":"用户中心"' >"$REQ"
call "建需求模块" POST /modules "$REQ" || exit 1
MODULE_ID="$(get data.id)"

body_of '"root":'"$PRODUCT_ID" '"type":"task"' '"name":"开发"' >"$REQ"
call "建任务模块" POST /modules "$REQ" || exit 1
TASK_MODULE_ID="$(get data.id)"

body_of \
	'"productId":'"$PRODUCT_ID" \
	'"moduleId":'"$MODULE_ID" \
	'"title":"登录支持手机号"' \
	'"spec":"用户可以用手机号 + 验证码登录。"' \
	'"verify":"手机号收到验证码后能登录成功。"' \
	'"pri":1' \
	'"estimate":3' \
	'"reviewers":['"$ADMIN_ID"']' >"$REQ"
call "提需求" POST /stories "$REQ" || exit 1
STORY_ID="$(get data.id)"
check_eq "需求初始状态（产品开了评审）" "$(get data.status)" "draft"

call_url "查需求" "$API/stories/$STORY_ID" || exit 1
STORY_VERSION="$(get data.version)"
check_nonempty "需求版本号" "$STORY_VERSION"

# ---------------------------------------------------------------------------
step "4. 需求评审：提交 → 通过"
# ---------------------------------------------------------------------------

body_of '"version":'"$STORY_VERSION" '"reviewers":['"$ADMIN_ID"']' >"$REQ"
call "提交评审" POST "/stories/$STORY_ID/submit-review" "$REQ"
call_url "复查需求状态" "$API/stories/$STORY_ID"
check_eq "提交后状态" "$(get data.status)" "reviewing"

body_of '"version":'"$STORY_VERSION" '"result":"pass"' '"comment":"方案清晰，通过"' >"$REQ"
call "评审通过" POST "/stories/$STORY_ID/review" "$REQ"
call_url "复查需求状态" "$API/stories/$STORY_ID"
check_eq "评审后状态" "$(get data.status)" "active"

# ---------------------------------------------------------------------------
step "5. 建项目 + 迭代 + 加成员"
# ---------------------------------------------------------------------------

body_of \
	'"name":"e2e 项目"' \
	'"code":"e2eprj"' \
	'"model":"scrum"' \
	'"productIds":['"$PRODUCT_ID"']' \
	'"po":'"$ADMIN_ID" \
	'"pm":'"$ADMIN_ID" \
	'"begin":"2026-01-01"' \
	'"end":"2026-12-31"' >"$REQ"
call "建项目" POST /projects "$REQ" || exit 1
PROJECT_ID="$(get data.id)"
check_nonempty "项目 id" "$PROJECT_ID"

body_of '"userId":'"$DEV_ID" '"role":"dev"' >"$REQ"
call "把研发拉进项目团队" POST "/projects/$PROJECT_ID/team" "$REQ"
body_of '"userId":'"$TESTER_ID" '"role":"qa"' >"$REQ"
call "把测试拉进项目团队" POST "/projects/$PROJECT_ID/team" "$REQ"

body_of \
	'"projectId":'"$PROJECT_ID" \
	'"name":"e2e 迭代一"' \
	'"type":"sprint"' \
	'"begin":"2026-01-01"' \
	'"end":"2026-02-28"' >"$REQ"
call "建迭代" POST /executions "$REQ" || exit 1
EXECUTION_ID="$(get data.id)"
check_nonempty "迭代 id" "$EXECUTION_ID"

body_of '"storyId":'"$STORY_ID" '"executionId":'"$EXECUTION_ID" >"$REQ"
call "需求排进迭代" POST "/projects/$PROJECT_ID/stories" "$REQ"

call_url "查项目需求" "$API/projects/$PROJECT_ID/stories"
check_eq "需求已排进迭代" "$(get data.0.executionId)" "$EXECUTION_ID"
check_eq "排进去的是那条需求" "$(get data.0.storyId)" "$STORY_ID"

# ---------------------------------------------------------------------------
step "6. 拆任务 → 开始 → 记工时 → 完成"
# ---------------------------------------------------------------------------

body_of \
	'"projectId":'"$PROJECT_ID" \
	'"executionId":'"$EXECUTION_ID" \
	'"productId":'"$PRODUCT_ID" \
	'"moduleId":'"$TASK_MODULE_ID" \
	'"storyId":'"$STORY_ID" \
	'"name":"实现手机号登录"' \
	'"type":"devel"' \
	'"pri":1' \
	'"estimate":'"$TASK_ESTIMATE" \
	'"assignedTo":'"$DEV_ID" >"$REQ"
call "拆任务（指派给研发）" POST /tasks "$REQ" || exit 1
TASK_ID="$(get data.id)"
check_eq "任务初始状态" "$(get data.status)" "wait"
check_eq "任务初始负责人" "$(get data.assignedTo)" "$DEV_ID"

# 指派那一刻，研发应该收到一条待办 —— 通知的触发原则是「通知下一个要动手的人」。
TOKEN="$DEV_TOKEN"
call_url "研发查未读通知" "$API/notices?unread=1&objectType=task&objectId=$TASK_ID"
check_eq "研发收到指派通知" "$(get data.list.0.title)" "任务指派给你"

TOKEN="$DEV_TOKEN"
call "研发开始任务" POST "/tasks/$TASK_ID/start" ""

body_of \
	'"consumed":'"$EFFORT_CONSUMED" \
	'"left":'"$EFFORT_LEFT" \
	'"work":"写完了短信通道与限流"' \
	'"date":"'"$TODAY"'"' >"$REQ"
call "研发记工时" POST "/tasks/$TASK_ID/efforts" "$REQ"

# 进度必须与工时公式一致，而不是与「状态」挂钩。
# 公式（见 service.hoursProgress）：left>0 时 round(consumed/(consumed+left)*100)；
# left==0 时 0（没干过）或 100（干完了）。
expect_progress() { # expect_progress <已消耗> <剩余>
	awk -v c="$1" -v l="$2" \
		'BEGIN{ if (l > 0) { printf "%d", c / (c + l) * 100 + 0.5 } else { printf "%d", (c == 0 ? 0 : 100) } }'
}

# 工时明细直接返回数组（不分页）：它天然是「这条任务下的一小串」。
call_url "查工时列表" "$API/tasks/$TASK_ID/efforts?objectType=task&objectId=$TASK_ID"
check_num "工时消耗" "$(get data.0.consumed)" "$EFFORT_CONSUMED"
check_num "报工后的剩余" "$(get data.0.left)" "$EFFORT_LEFT"

call_url "复查处在中途的任务" "$API/tasks/$TASK_ID"
check_eq "中途状态" "$(get data.status)" "doing"
check_eq "中途 progress 合于工时公式" "$(get data.progress)" \
	"$(expect_progress "$EFFORT_CONSUMED" "$EFFORT_LEFT")"

# 「还剩工时却要完成」会被拒 —— 这两件事互相矛盾，进度会算出个半吊子。
body_of '"comment":"忘了报剩余工时"' >"$REQ"
call_status "剩余工时非 0 时拒绝完成" POST "/tasks/$TASK_ID/finish" "409" "$REQ"

# 正常用法：完成时顺手把剩下的工时一次报完（左式等价于 newLeft = 3 - 3 = 0）。
body_of '"consumed":'"$EFFORT_LEFT" '"comment":"收尾、自测、提测"' >"$REQ"
call "研发完成任务（顺手报完剩余工时）" POST "/tasks/$TASK_ID/finish" "$REQ"

call_url "复查任务" "$API/tasks/$TASK_ID"
check_eq "任务最终状态" "$(get data.status)" "done"
check_num "任务消耗工时" "$(get data.consumed)" "$((EFFORT_CONSUMED + EFFORT_LEFT))"
check_num "任务剩余工时" "$(get data.left)" "0"
check_eq "完成时 progress = 100" "$(get data.progress)" \
	"$(expect_progress "$((EFFORT_CONSUMED + EFFORT_LEFT))" 0)"

call_url "复查工时明细" "$API/tasks/$TASK_ID/efforts?objectType=task&objectId=$TASK_ID"
# 列表按时间倒序：最新一条在最前。
check_num "收尾那条工时" "$(get data.0.consumed)" "$EFFORT_LEFT"
check_num "收尾后剩余归零" "$(get data.0.left)" "0"
check_num "更早那条仍是中途的 5/3" "$(get data.1.consumed)" "$EFFORT_CONSUMED"
check_eq "只有两条工时" "$(get data.2.id)" ""

# 完成即「下一步在提需求的人手上」，于是负责人之外的人收到待办。
TOKEN="$ADMIN_TOKEN"
call_url "管理员查任务通知" "$API/notices?unread=1&objectType=task&objectId=$TASK_ID"
check_eq "管理员收到完成通知" "$(get data.list.0.title)" "任务已完成"

# ---------------------------------------------------------------------------
step "7. 缺陷：报 → 指派 → 解决 → 关闭 → 重新激活"
# ---------------------------------------------------------------------------

TOKEN="$TESTER_TOKEN"
# 先报进池子（不指定人），再指派 —— 这是最常见的两步走：
# 报的人未必知道该谁修。也让「指派」这个动作单独留一条痕迹。
body_of \
	'"productId":'"$PRODUCT_ID" \
	'"projectId":'"$PROJECT_ID" \
	'"executionId":'"$EXECUTION_ID" \
	'"storyId":'"$STORY_ID" \
	'"title":"验证码 60 秒内可以重复发送"' \
	'"steps":"1. 输入手机号\n2. 连点发送\n3. 第二条也发出去了"' \
	'"type":"codeerror"' \
	'"severity":2' \
	'"pri":1' \
	'"openedBuild":"v1.0.0"' >"$REQ"
call "测试报缺陷" POST /bugs "$REQ" || exit 1
BUG_ID="$(get data.id)"
check_eq "缺陷初始状态" "$(get data.status)" "active"
check_eq "报的时候不指定人" "$(get data.assignedTo)" "0"

body_of '"assignedTo":'"$DEV_ID" '"comment":"这个应该是短信通道的问题"' >"$REQ"
call "把缺陷指派给研发" POST "/bugs/$BUG_ID/assign" "$REQ"
call_url "复查缺陷" "$API/bugs/$BUG_ID"
check_eq "缺陷指派给研发" "$(get data.assignedTo)" "$DEV_ID"

TOKEN="$DEV_TOKEN"
call_url "研发查缺陷通知" "$API/notices?unread=1&objectType=bug&objectId=$BUG_ID"
check_eq "研发收到缺陷通知" "$(get data.list.0.title)" "缺陷指派给你"

body_of '"resolution":"fixed"' '"resolvedBuild":"v1.0.1"' '"comment":"加了发送间隔限制"' >"$REQ"
call "研发解决缺陷" POST "/bugs/$BUG_ID/resolve" "$REQ"
call_url "复查缺陷状态" "$API/bugs/$BUG_ID"
check_eq "解决后状态" "$(get data.status)" "resolved"
check_eq "解决方案" "$(get data.resolution)" "fixed"

TOKEN="$TESTER_TOKEN"
call_url "测试查缺陷通知" "$API/notices?unread=1&objectType=bug&objectId=$BUG_ID"
check_eq "测试收到待验证通知" "$(get data.list.0.title)" "缺陷已解决"

body_of '"comment":"验证通过"' >"$REQ"
call "测试关闭缺陷" POST "/bugs/$BUG_ID/close" "$REQ"
call_url "复查缺陷状态" "$API/bugs/$BUG_ID"
check_eq "关闭后状态" "$(get data.status)" "closed"

# 「重新激活」不是新开一条缺陷，而是把同一条推回 active 并计一次激活次数。
body_of '"assignedTo":'"$DEV_ID" '"comment":"线上又复现了"' >"$REQ"
call "测试重新激活缺陷" POST "/bugs/$BUG_ID/activate" "$REQ"
call_url "复查缺陷状态" "$API/bugs/$BUG_ID"
check_eq "激活后状态" "$(get data.status)" "active"
check_eq "激活次数" "$(get data.activatedCount)" "1"
check_eq "激活后指派给" "$(get data.assignedTo)" "$DEV_ID"

# 同一个对象上只留一条未读：研发看到的是「现在该我处理它了」，
# 而不是一摞「曾被指派过」的流水。
TOKEN="$DEV_TOKEN"
call_url "研发查缺陷待办" "$API/notices?unread=1&objectType=bug&objectId=$BUG_ID"
check_eq "同一缺陷只剩一条未读" "$(get data.total)" "1"
check_eq "未读被更新为最新待办" "$(get data.list.0.title)" "缺陷重新激活"

# ---------------------------------------------------------------------------
step "8. 操作历史与聚合"
# ---------------------------------------------------------------------------

call_url "查任务操作历史" "$API/actions?objectType=task&objectID=$TASK_ID&page=1&size=50"
ACTIONS="$(dump data.list)"
for verb in opened started RecordWorkhour finished; do
	check_has "任务历史含 $verb" "$ACTIONS" "\"action\":\"$verb\""
done

call_url "查缺陷操作历史" "$API/actions?objectType=bug&objectID=$BUG_ID&page=1&size=50"
ACTIONS="$(dump data.list)"
for verb in opened assigned resolved closed activated; do
	check_has "缺陷历史含 $verb" "$ACTIONS" "\"action\":\"$verb\""
done

# 任务工时 → 项目工时：聚合口径必须与任务自身一致。
ALL_CONSUMED=$((EFFORT_CONSUMED + EFFORT_LEFT))
call_url "查项目详情" "$API/projects/$PROJECT_ID"
check_num "项目消耗工时" "$(get data.consumed)" "$ALL_CONSUMED"
check_num "项目剩余工时" "$(get data.left)" "0"
check_eq "项目 progress" "$(get data.progress)" "$(expect_progress "$ALL_CONSUMED" 0)"

# ---------------------------------------------------------------------------
step "9. 工作台、通知与燃尽图"
# ---------------------------------------------------------------------------

TOKEN="$DEV_TOKEN"
call_url "研发看工作台" "$API/workbench"
# 任务已完成、缺陷被重新激活并派回给他：待办里只剩缺陷。
check_eq "工作台待办任务数" "$(get data.counts.taskDoing)" "0"
check_eq "工作台待办缺陷数" "$(get data.counts.bugActive)" "1"
# 两条未读：任务指派（admin 发的）+ 缺陷重新激活（覆盖了缺陷指派那条）。
check_eq "工作台未读通知数" "$(get data.counts.noticeUnread)" "2"

# 燃尽图要 report 权限，用测试身份看。
TOKEN="$TESTER_TOKEN"
call_url "查燃尽图" "$API/workbench/burndown?executionId=$EXECUTION_ID&begin=$BURN_BEGIN&end=$BURN_END"
check_eq "燃尽图范围" "$(get data.scope)" "execution"
check_num "燃尽图总工时" "$(get data.totalEstimate)" "$TASK_ESTIMATE"
check_num "燃尽图已消耗" "$(get data.totalConsumed)" "$ALL_CONSUMED"
check_eq "燃尽图按天补齐" "$(json_get "$HTTP_BODY" 'data.days.0.date')" "$BURN_BEGIN"

call_url "测试标记通知已读" "$API/notices?page=1&size=1"
NOTICE_ID="$(get data.list.0.id)"
if [ -n "$NOTICE_ID" ]; then
	body_of '"ids":['"$NOTICE_ID"']' >"$REQ"
	call "标记已读" POST /notices/read "$REQ"
else
	ok "测试没有未读通知，跳过标记（不影响结论）"
fi

# ---------------------------------------------------------------------------
step "10. 可见性与越权"
# ---------------------------------------------------------------------------

# 一个跟项目无关的人，不该看到私有产品下的东西。
# 建号得用管理员身份：上一步切换过 TOKEN，这里显式切回来。
TOKEN="$ADMIN_TOKEN"
body_of \
	'"account":"e2e_outsider"' \
	'"password":"'"$DEV_PASSWORD"'"' \
	'"realname":"e2e 局外人"' \
	'"groups":['"$DEV_GROUP"']' >"$REQ"
call "建第三个用户（局外人）" POST /users "$REQ" || exit 1
OUTSIDER_TOKEN="$(login_as e2e_outsider "$DEV_PASSWORD")"

# 把产品改成私有、只给研发白名单：局外人此后应当既看不到需求、也打不开它的评论。
body_of \
	'"name":"e2e 产品"' \
	'"code":"e2eprd"' \
	'"po":'"$ADMIN_ID" \
	'"acl":"private"' \
	'"whitelist":['"$DEV_ID"']' >"$REQ"
call "把产品改为私有" PUT "/products/$PRODUCT_ID" "$REQ"

TOKEN="$OUTSIDER_TOKEN"
call_url "局外人查需求列表" "$API/stories?productId=$PRODUCT_ID"
check_eq "私有产品的需求不可见" "$(get data.total)" "0"
# 列表里没有还不够 —— 直接按 id 打开也必须打不开，否则「看不见」只是层皮。
call_status "局外人直接打开需求详情" GET "$API/stories/$STORY_ID" "403"
# 评论挂在需求上，走的是同一套可见性网关：看不到需求就不该读到它的讨论。
call_status "局外人读需求的评论" GET "$API/comments?objectType=story&objectID=$STORY_ID" "403"

# 白名单里的人照样进得去 —— 防止过滤写过头，把有权的人也挡在外面。
TOKEN="$DEV_TOKEN"
call_status "白名单内可访问需求" GET "$API/stories/$STORY_ID" "200"

# ---------------------------------------------------------------------------
# 11. 测试用例与测试单（用例评审 → 纳入 → 执行 → 结单）
# ---------------------------------------------------------------------------

step "11. 测试用例与测试单"

# 这一节自带一个产品：上一节把 PRODUCT_ID 改成了私有，拿它做测试域的用例
# 会因为「看不见这个产品」而失败，那是可见性的正确行为，不是测试域的 bug。
# 用管理员建产品与模块树（module-manage 不在测试组的权限里）。
TOKEN="$ADMIN_TOKEN"
body_of '"name":"登录测试域产品"' '"code":"E2ELOGIN"' '"status":"normal"' >"$REQ"
call "建测试域产品" POST /products "$REQ" || exit 1
TEST_PRODUCT_ID="$(get data.id)"

# 用例模块树：同一产品下需求/任务/缺陷/用例是四棵独立的树。
body_of \
	'"root":'"$TEST_PRODUCT_ID" \
	'"type":"case"' \
	'"name":"登录用例组"' >"$REQ"
call "建用例模块树" POST /modules "$REQ" || exit 1
CASE_MODULE_ID="$(get data.id)"

# 建用例（两步）。步骤顺序由后端按提交顺序重排，前端传的序号不作数。
TOKEN="$TESTER_TOKEN"
body_of \
	'"productId":'"$TEST_PRODUCT_ID" \
	'"moduleId":'"$CASE_MODULE_ID" \
	'"title":"手机号登录失败时提示口令不正确"' \
	'"type":"feature"' \
	'"pri":1' \
	'"precondition":"已有一个未注册手机号"' \
	'"steps":[{"step":"打开登录页","expect":"出现账号与口令输入框"},{"step":"输入错误口令并提交","expect":"提示“口令不正确”"}]' >"$REQ"
call "建用例" POST /testcases "$REQ" || exit 1
CASE_ID="$(get data.id)"
check_eq "用例初始评审阶段" "$(get data.stage)" "wait"
check_eq "用例初始版本" "$(get data.version)" "1"
check_eq "用例步骤数" "$(get data.stepCount)" "2"
check_eq "用例编号" "$(get data.code)" "CASE-$CASE_ID"

# 没步骤的用例无法逐步判定，必须拒。
body_of '"productId":'"$TEST_PRODUCT_ID" '"title":"没有步骤的用例"' >"$REQ"
call_status "建无步骤用例被拒" POST "/testcases" "400" "$REQ"

# 拿需求树的节点当用例模块：四棵树各自独立，必须拒。
body_of \
	'"productId":'"$TEST_PRODUCT_ID" \
	'"moduleId":'"$MODULE_ID" \
	'"title":"挂错模块树的用例"' \
	'"steps":[{"step":"a","expect":"b"}]' >"$REQ"
call_status "用例挂到需求树被拒" POST "/testcases" "400" "$REQ"

# 未评审通过的用例不该进测试单。
body_of '"productId":'"$TEST_PRODUCT_ID" '"name":"不该收到未评审用例的单"' '"caseIds":['"$CASE_ID"']' >"$REQ"
call_status "未确认用例纳入被拒" POST "/testtasks" "400" "$REQ"

# 提交评审 → 评审中。全量内容要在请求体里带一遍（PUT 是整体覆盖语义）。
case_full_body() { # case_full_body <标题> —— 复用同一份两步内容
	body_of \
		'"productId":'"$TEST_PRODUCT_ID" \
		'"moduleId":'"$CASE_MODULE_ID" \
		'"title":"'"$1"'"' \
		'"type":"feature"' \
		'"pri":1' \
		'"precondition":"已有一个未注册手机号"' \
		'"steps":[{"step":"打开登录页","expect":"出现账号与口令输入框"},{"step":"输入错误口令并提交","expect":"提示口令不正确"}]' >"$REQ"
}

body_of '"comment":"请产品确认覆盖是否足够"' >"$REQ"
call "提交用例评审" POST "/testcases/$CASE_ID/submit-review" "$REQ"
case_full_body "评审中偷偷改标题"
call_status "评审中不能编辑用例" PUT "/testcases/$CASE_ID" "409" "$REQ"

# 评审通过由「有 testcase-review 权的人」出结论；测试组有。
body_of '"result":"pass"' '"comment":"覆盖够了"' >"$REQ"
call "用例评审通过" POST "/testcases/$CASE_ID/review" "$REQ"
check_eq "评审后阶段" "$(get data.stage)" "confirmed"

# 建测试单并纳入这条用例。
body_of '"productId":'"$TEST_PRODUCT_ID" '"name":"v1.0 登录回归"' '"pri":1' >"$REQ"
call "建测试单" POST /testtasks "$REQ" || exit 1
TASK_TEST_ID="$(get data.id)"
check_eq "测试单初始状态" "$(get data.status)" "wait"

body_of '"caseIds":['"$CASE_ID"']' >"$REQ"
call "纳入用例" POST "/testtasks/$TASK_TEST_ID/cases" "$REQ"
check_eq "单内用例数" "$(get data.caseCount)" "1"
check_eq "单内锁定的用例版本" "$(get data.cases.0.caseVersion)" "1"

# 开始 → 记录一次失败（步骤的实际结果只给第 2 步）。
call "开始测试单" POST "/testtasks/$TASK_TEST_ID/start" ""
body_of \
	'"result":"fail"' \
	'"note":"提示文案不对"' \
	'"steps":[{"order":2,"actual":"提示的是“登录失败”"}]' \
	'"duration":0.5' >"$REQ"
call "记录执行失败" POST "/testtasks/$TASK_TEST_ID/cases/$CASE_ID/run" "$REQ" || exit 1
# 步骤的原文由后端按用例内容回填：客户端只传实际结果，
# 否则一份被改过的步骤描述可以伪装成「当时跑的就是这样」。
check_nonempty "快照含第 2 步" "$(get data.steps.1.step)"
check_eq "快照不含第 3 步" "$(get data.steps.2.step)" ""
check_eq "第 1 步原文由后端回填" "$(get data.steps.0.step)" "打开登录页"
check_eq "第 1 步没传实际结果应为空" "$(get data.steps.0.actual)" ""
check_eq "第 2 步的实际结果被记录" "$(get data.steps.1.actual)" "提示的是“登录失败”"

call_url "单内用例的最新结论" "$API/testtasks/$TASK_TEST_ID"
check_eq "单内用例最新结论" "$(get data.cases.0.lastResult)" "fail"
check_eq "单内执行汇总的失败数" "$(get data.summary.fail)" "1"

# 修好后复测通过：执行历史是追加的，两次结果都要能查到。
body_of '"result":"pass"' '"note":"文案已修正"' '"duration":0.3' >"$REQ"
call "复测通过" POST "/testtasks/$TASK_TEST_ID/cases/$CASE_ID/run" "$REQ"
call_url "查这条用例的执行历史" "$API/testtasks/$TASK_TEST_ID/runs?caseId=$CASE_ID"
check_eq "执行记录条数" "$(get data.total)" "2"
# 最新一次覆盖单内状态，历史仍是两条。
call_url "复测后的单内汇总" "$API/testtasks/$TASK_TEST_ID"
check_eq "复测后通过数" "$(get data.summary.pass)" "1"
check_eq "复测后失败数" "$(get data.summary.fail)" "0"

# 结单必须给结论。传一个空对象而不是不传体：要的是「result 必填」这个 400，
# 不是「请求体缺失」那个 400 —— 两者状态码相同，拒的原因完全不同。
printf '{}' >"$REQ"
call_status "不给结论不能结单" POST "/testtasks/$TASK_TEST_ID/finish" "400" "$REQ"
body_of '"result":"pass"' '"report":"登录相关用例全部通过"' >"$REQ"
call "结单" POST "/testtasks/$TASK_TEST_ID/finish" "$REQ"
check_eq "结单后状态" "$(get data.status)" "closed"
check_eq "结单结论" "$(get data.result)" "pass"

# 已结单不再接受执行结果，也不该被一键删掉（执行记录是测试证据）。
body_of '"result":"pass"' >"$REQ"
call_status "已结单不能记结果" POST "/testtasks/$TASK_TEST_ID/cases/$CASE_ID/run" "409" "$REQ"
call_status "有执行记录不能删单" DELETE "/testtasks/$TASK_TEST_ID" "409"

# 执行动作要留痕：时间线上能看到那次 run。
call_url "测试单的操作历史" "$API/actions?objectType=testtask&objectID=$TASK_TEST_ID"
check_nonempty "历史里有开单记录" "$(get data.list.0.id)"

# 用例列表能按阶段筛出这条已确认的用例。
call_url "按阶段筛用例" "$API/testcases?stage=confirmed&productId=$TEST_PRODUCT_ID"
check_num "已确认用例数" "$(get data.total)" "1"

# 测试单可以评论：评论的对象类型是一份白名单，漏加 testtask 会得到 400，
# 而前端详情页的评论区会一直空着（没人会想到是后端白名单漏了一项）。
TOKEN="$TESTER_TOKEN"
body_of \
	'"objectType":"testtask"' \
	'"objectID":'"$TASK_TEST_ID" \
	'"content":"回归结论已确认"' >"$REQ"
call "给测试单写评论" POST /comments "$REQ"
call_url "读测试单的评论" "$API/comments?objectType=testtask&objectID=$TASK_TEST_ID"
check_num "测试单评论数" "$(get data.total)" "1"

# 测试单的执行汇总走的是 testrun-record 权限点：研发组没有它，应当被挡。
TOKEN="$DEV_TOKEN"
body_of '"result":"pass"' >"$REQ"
call_status "无 testrun-record 权限不能记结果" \
	POST "/testtasks/$TASK_TEST_ID/cases/$CASE_ID/run" "403" "$REQ"

# ---------------------------------------------------------------------------
# 收尾
# ---------------------------------------------------------------------------

printf '\n%s----------------------------------------%s\n' "$CLR_DIM" "$CLR_RST"
if [ "$FAILED" -eq 0 ]; then
	printf '%s全部通过：%d 项断言%s\n' "$CLR_OK" "$PASSED" "$CLR_RST"
	exit 0
fi
printf '%s失败 %d 项，通过 %d 项%s\n' "$CLR_BAD" "$FAILED" "$PASSED" "$CLR_RST"
[ -n "$SERVER_PID" ] && printf '服务日志：%s\n' "$SERVER_LOG"
exit 1
