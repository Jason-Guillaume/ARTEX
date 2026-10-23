# BOLA / IDOR 检查清单

## 前置：收集测试数据

### 1. 收集测试账户

至少 2 个不同用户（不同资源所有者），最好不同租户。

```bash
# 用户 A 的 /me
curl -sk -H @auth/headers_user_a.txt "$BASE/api/me" | jq '{sub, role, tenant_id}'
# 用户 B
curl -sk -H @auth/headers_user_b.txt "$BASE/api/me" | jq '{sub, role, tenant_id}'
```

### 2. 收集测试资源

每个用户至少 1 个自有资源 id：

```bash
# 用户 A 的资源
curl -sk -H @auth/headers_user_a.txt "$BASE/api/orders" | jq -r '.data[0].id' > poc/a_ids.txt
# 用户 B 的资源
curl -sk -H @auth/headers_user_b.txt "$BASE/api/orders" | jq -r '.data[0].id' > poc/b_ids.txt
```

---

## 三态对照（BOLA 核心）

对每个资源 id 类 API（`/xxx/{id}` 或 `?id=xxx`）：

| 测试 | 身份 | 请求目标 | 期望 |
|---|---|---|---|
| T1 | 匿名 | A 的资源 | 401/403 |
| T2 | 匿名 | B 的资源 | 401/403 |
| T3 | 用户 A | A 的资源 | 200 + A 数据 |
| T4 | 用户 A | B 的资源 | **403（若 200 = BOLA）** |
| T5 | 用户 B | A 的资源 | **403（若 200 = BOLA）** |
| T6 | 管理员 | A 的资源 | 200 |
| T7 | 管理员 | B 的资源 | 200 |

---

## 自动化脚本

```bash
#!/bin/bash
# poc/bola_matrix.sh
BASE="https://<target>"
IDS_A=$(cat poc/a_ids.txt)
IDS_B=$(cat poc/b_ids.txt)

test_ep() {
  local identity=$1  # anon, user_a, user_b, admin
  local id=$2
  local ep=$3
  local method=${4:-GET}
  
  local hdr=""
  [ "$identity" = "user_a" ] && hdr="auth/headers_user_a.txt"
  [ "$identity" = "user_b" ] && hdr="auth/headers_user_b.txt"
  [ "$identity" = "admin" ] && hdr="auth/headers_admin.txt"
  
  local outfile="evidence/bola/${identity}_${id}_$(basename $ep).json"
  mkdir -p evidence/bola
  
  local reqargs=("$BASE$ep/$id" -o "$outfile" -w "%{http_code} %{size_download}")
  [ -n "$hdr" ] && reqargs=("-H" "@$hdr" "${reqargs[@]}")
  [ "$method" != "GET" ] && reqargs=("-X" "$method" "${reqargs[@]}")
  
  local result=$(curl -sk "${reqargs[@]}")
  echo "$identity,$id,$ep,$result"
}

for ep in /api/orders /api/users /api/documents /api/invoices; do
  for id in "$IDS_A" "$IDS_B"; do
    test_ep anon $id $ep
    test_ep user_a $id $ep
    test_ep user_b $id $ep
    test_ep admin $id $ep
  done
done > evidence/bola/matrix.csv
```

---

## 分析矩阵

```bash
# 提取所有 200 响应
awk -F, '$4 ~ /200/ {print $1,$2,$3}' evidence/bola/matrix.csv
# 判定 BOLA：
# - anon 有 200 → 严重（未授权）
# - user_a 请求 B 的资源 200 → BOLA
# - user_b 请求 A 的资源 200 → BOLA
# - admin 请求都 200 → 合法
```

---

## 特殊 ID 变体测试

### UUID
- 不可枚举，先通过列表接口拿 UUID 清单，再交叉请求

### 自增整数
- 遍历 id+1、id+100、id+1000

### 时间戳
- 从已知时间戳开始遍历

### Hash 类 ID
- 尝试字典：admin, test, root, guest, demo

### ID 编码
- Base64 编码 / 解码
- URL 编码

```bash
ID="12345"
B64=$(echo -n $ID | base64)
curl -sk -H @auth/headers_user_a.txt "$BASE/api/user/$B64"
curl -sk -H @auth/headers_user_a.txt "$BASE/api/user/$(python3 -c "print('$ID'.encode().hex())")"
```

---

## 参数位置变体

| 位置 | 示例 |
|---|---|
| Path | `/api/user/{id}` |
| Query | `?id=xxx` |
| Body | `{"id": "xxx"}` |
| Header | `X-User-Id: xxx` |
| Cookie | `uid=xxx` |

每种位置都测：

```bash
# Query
curl -sk -H @auth/headers_user_a.txt "$BASE/api/user?id=$ID_B"
# Header
curl -sk -H @auth/headers_user_a.txt -H "X-User-Id: $ID_B" "$BASE/api/user/me"
```

---

## 批量接口专项

```bash
# 批量查询
curl -sk -H @auth/headers_user_a.txt -X POST -H "Content-Type: application/json" \
  -d "{\"ids\":[\"$ID_A\",\"$ID_B\"]}" "$BASE/api/users/batch"
```

若返回 A 和 B 的数据 → 批量 BOLA。

---

## 响应分析

### 关键字段对比

```bash
diff <(jq '.data.name' evidence/bola/user_a_${ID_A}_user.json) \
     <(jq '.data.name' evidence/bola/user_a_${ID_B}_user.json)
```

若返回相同结构但字段值属于 B → BOLA confirmed。

### 软返回（返回 200 但内容空）

- 若返回 200 + 空 body → 需交叉验证其他字段
- 若返回 200 + `{data: null}` → 仍可能是软 BOLA（服务端知道 id 存在但不返回数据）

### 敏感信息

响应中的敏感字段：
- `username`、`email`、`phone`、`address`、`id_card`
- `password_hash`、`salt`、`api_key`、`token`
- `bank_card`、`balance`、`transaction`

---

## 报告格式

```markdown
### F<xx>: BOLA 越权读取

- **位置**：`GET /api/user/{id}`
- **身份 A**：用户 A（uid=123）
- **身份 B**：用户 B（uid=456）
- **攻击步骤**：
  1. 用户 A 用合法 token 请求 `/api/user/456`（B 的 id）
  2. 服务器返回 B 的完整数据
- **影响**：任何登录用户可通过遍历 id 读取全库用户数据
- **CVSS**：AV:N/AC:L/PR:L/UI:N/S:C/C:H/I:N/A:N → **7.5**
- **CWE**：CWE-639
- **OWASP**：A01:2021 Broken Access Control / API1:2019 BOLA
- **修复建议**：
  1. 服务端校验请求者是否为资源所有者
  2. 或返回 404 而非 403（避免 id 枚举）
  3. 使用不可枚举的 ID（UUID）
  4. 添加审计日志
```
