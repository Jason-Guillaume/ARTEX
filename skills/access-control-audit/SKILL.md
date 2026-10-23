---
name: access-control-audit
description: 访问控制与多身份测试。三态对照法（匿名/伪造/合法）、垂直越权、水平越权（BOLA/IDOR）、多租户隔离、批量接口、跨资源操作。当任务目标是"验证权限边界、多用户数据隔离"时启用。
---
---
name: access-control-audit
description: 访问控制审计。三态对照、BOLA/IDOR/BFLA、多租户隔离、批量接口、跨资源越权。
---

# access-control-audit — 访问控制审计

**核心方法论**：**三态对照**。同一接口 × 3 个身份 × N 个资源组合，逐一对比响应。
**依赖**：至少 2 个不同权限的测试账户；`api-security-audit` Phase 1 的 BOLA 结果可复用。
**禁止**：不制造不可恢复的数据、不写破坏性内容到他人账户。

---

## Phase 0 — 身份准备

必须至少 4 个"身份视角"：

| 视角 | 说明 | headers 文件 |
|---|---|---|
| 匿名 | 无 token | 空 |
| 伪造 | 无效/过期/其他租户 token | `headers_fake.txt` |
| 普通用户 A | 租户 T1，资源所有者 | `headers_user_a.txt` |
| 普通用户 B | 租户 T1，另一用户 | `headers_user_b.txt` |
| 管理员 | 全局权限 | `headers_admin.txt` |
| 跨租户用户 | 租户 T2 的用户 | `headers_user_t2.txt`（可选） |

**前置**：每个身份必须能通过 `/me` 验证身份与权限：

```bash
for h in auth/headers_*.txt; do
  echo "=== $h ==="
  curl -sk -H @$h "$BASE/api/me" | jq '{sub,role,tenant_id,scopes}'
done
```

---

## Phase 1 — 三态对照矩阵（核心）

### 1.1 建表

对每个 API 端点做以下测试：

| 身份 | 请求 | 期望响应 |
|---|---|---|
| 匿名 | `GET /api/user/{own_id}` | 401 或 403 |
| 匿名 | `GET /api/user/{other_id}` | 401 或 403 |
| 伪造 | 同匿名 | 401 |
| 用户 A | `GET /api/user/{A_id}` | 200 + A 数据 |
| 用户 A | `GET /api/user/{B_id}` | **403（若 200 = BOLA）** |
| 用户 B | `GET /api/user/{A_id}` | 403（若 200 = BOLA） |
| 管理员 | `GET /api/user/{A_id}` | 200（合法） |
| 管理员 | `GET /api/user/{B_id}` | 200 |

### 1.2 自动化

```bash
# 生成对照矩阵 CSV
ENDPOINTS="GET /api/user /GET /api/user/{id} /GET /api/orders /GET /api/orders/{id} /POST /api/orders ..."
IDS="A_id B_id"
for ep in ...; do for identity in anon fake A B admin; do for id in A_id B_id; do
  code=$(curl -sk -H @auth/headers_$identity.txt "$BASE$ep/$id" -o "evidence/http/${ep}_$identity_$id.json" -w "%{http_code}")
  size=$(wc -c < "evidence/http/${ep}_$identity_$id.json")
  echo "$identity,$id,$ep,$code,$size"
done; done; done
```

### 1.3 分析

- 响应 **code** 差异（401 vs 403 vs 200）
- 响应 **长度** 差异（相同资源，不同身份返回不同长度可能是设计）
- 响应 **敏感字段** 泄露（如响应里含其他用户 id / tenant）

---

## Phase 2 — 垂直越权（Privilege Escalation）

### 2.1 找高权限接口

路径 / 关键词：`admin`、`super`、`system`、`config`、`audit`、`user/add`、`role/grant`、`permission/*`、`tenant/manage`、`report/export`。

### 2.2 越权测试

```bash
for ep in /api/admin/users /api/system/config /api/audit/log /api/tenant/manage; do
  echo "=== $ep ==="
  curl -sk -H @auth/headers_user_a.txt -X GET "$BASE$ep" -o /dev/null -w "%{http_code}\n"
  curl -sk -H @auth/headers_user_a.txt -X POST -H "Content-Type: application/json" -d '{}' "$BASE$ep" -o /dev/null -w "%{http_code}\n"
  curl -sk -H @auth/headers_user_a.txt -X PUT -H "Content-Type: application/json" -d '{}' "$BASE$ep" -o /dev/null -w "%{http_code}\n"
  curl -sk -H @auth/headers_user_a.txt -X DELETE "$BASE$ep" -o /dev/null -w "%{http_code}\n"
done
```

**判定**：普通用户返回 200 → BFLA confirmed，critical。

### 2.3 权限字符串篡改

Payload 里包含 `role`、`permissions`、`is_admin`、`group_id` 等字段时，直接篡改：

```bash
curl -sk -H @auth/headers_user_a.txt -X PUT -H "Content-Type: application/json" \
  -d '{"role":"admin","permissions":["*"]}' \
  "$BASE/api/user/${A_ID}" -o /dev/null -w "%{http_code}\n"
# 重新验证身份
curl -sk -H @auth/headers_user_a.txt "$BASE/api/me" | jq '.role,.permissions'
```

**判定**：role 变成 admin → 用户可自我提权（critical）。

---

## Phase 3 — 水平越权（BOLA / IDOR）

见 `api-security-audit` Phase 1。这里补充**批量场景**：

### 3.1 批量查询

```bash
# 一次查多个 ID，看是否都授权
curl -sk -H @auth/headers_user_a.txt -X POST -H "Content-Type: application/json" \
  -d '{"ids":["A_id","B_id","admin_id"]}' \
  "$BASE/api/users/batch" | jq '.data[]|{id,name}'
```

**判定**：返回了 B 和 admin 数据 → 批量接口越权（比单条 BOLA 更严重，因为攻击者一次拿全）。

### 3.2 数组 / 逗号分隔

- `?ids=A_id,B_id,C_id`
- `?ids[]=A_id&ids[]=B_id`
- `?user_ids=A,B,C`

### 3.3 分页遍历

- `?page=1&page_size=1000`（无上限）
- 遍历 `?page=1,2,3...`，每页 100 → 拿全库

### 3.4 ID 混淆

- `id=1` 和 `id="1"` 是否不同处理
- `id=1 OR 1=1`（SQL 类）
- `id=1&admin=true`
- 大小写：`/API/User/1`

---

## Phase 4 — 多租户隔离

**关键**：不同租户的用户能否看到/操作对方数据。

### 4.1 直接探测

若 API 响应中带 `tenant_id`，观察：
- 用户 A 的列表接口是否只返回 tenant=T1 数据
- 用户 A 是否可通过篡改 `tenant_id` header / body 看到 T2 数据

```bash
# 尝试在 header 里加 tenant
curl -sk -H @auth/headers_user_a.txt -H "X-Tenant-Id: T2" "$BASE/api/users" | jq '.data[].tenant_id'
# 尝试在 body 里加 tenant
curl -sk -H @auth/headers_user_a.txt -X POST -H "Content-Type: application/json" \
  -d '{"tenant_id":"T2","page":1}' "$BASE/api/users" | jq '.data[].tenant_id'
```

**判定**：返回了非本租户数据 → 严重多租户隔离缺陷。

### 4.2 跨租户直接操作

- 用户 A 修改用户 B 的账号（BOLA + 跨租户）
- 用户 A 删除用户 B 的资源
- 用户 A 使用用户 B 的 refresh_token

### 4.3 缓存污染

- 用户 A 请求某个 key，服务器写入缓存
- 用户 B 请求同一 key，若返回 A 的缓存 → 缓存未隔离

```bash
curl -sk -H @auth/headers_user_a.txt "$BASE/api/widget?id=1" > /tmp/a.json
sleep 1
curl -sk -H @auth/headers_user_b.txt "$BASE/api/widget?id=1" > /tmp/b.json
diff /tmp/a.json /tmp/b.json
```

---

## Phase 5 — 批量接口与幂等性

### 5.1 幂等性

- POST 同一请求发 2 次，看是否创建 2 条记录（应幂等则失败）
- 使用 Idempotency-Key header：
  - 无 key → 幂等性依赖？
  - 有 key 但服务端不检查 → 幂等性失效

```bash
for i in 1 2 3; do
  curl -sk -H @auth/headers_user_a.txt -X POST -H "Content-Type: application/json" \
    -H "Idempotency-Key: test-key-$RANDOM" \
    -d '{"amount":1}' "$BASE/api/orders" -o /dev/null -w "%{http_code}\n"
done
# 检查订单是否创建了 3 条
curl -sk -H @auth/headers_user_a.txt "$BASE/api/orders?page_size=5" | jq '.data | length'
```

### 5.2 批量操作的资源上限

- `ids[]` 数组长度上限：10000 个 id 是否被接受 → 资源耗尽
- 大 payload：请求 body 大小上限

---

## Phase 6 — 状态变更的鉴权

**问题**：创建时的鉴权 ≠ 修改时的鉴权。

```bash
# 用户 A 创建订单 → 拿到 order_id
ORDER_A=$(curl -sk -H @auth/headers_user_a.txt -X POST -d '{}' "$BASE/api/orders" | jq -r '.id')

# 用户 B 修改 A 的订单
curl -sk -H @auth/headers_user_b.txt -X PUT -H "Content-Type: application/json" \
  -d '{"status":"cancelled"}' "$BASE/api/orders/$ORDER_A" -o /dev/null -w "%{http_code}\n"

# 检查订单状态
curl -sk -H @auth/headers_user_a.txt "$BASE/api/orders/$ORDER_A" | jq '.status'
```

**判定**：状态被修改 → 授权缺陷（写操作未校验资源所有权）。

---

## Phase 7 — 越权访问测试用例（可直接执行）

保存为 `poc/access_control_matrix.sh`：

```bash
#!/bin/bash
# 三态对照自动化脚本
ENDPOINTS=(
  "GET /api/users"
  "GET /api/users/A_ID"
  "POST /api/users"
  "GET /api/orders"
  "PUT /api/orders/A_ORDER_ID"
  "DELETE /api/orders/A_ORDER_ID"
  "GET /api/admin/users"
  "GET /api/audit/log"
)
IDENTITIES=("anon" "fake" "user_a" "user_b" "admin")

for ep in "${ENDPOINTS[@]}"; do
  for id in "${IDENTITIES[@]}"; do
    hdr="auth/headers_${id}.txt"
    [ "$id" = "anon" ] && hdr=""
    code=$(curl -sk ${hdr:+-H @$hdr} $ep "$BASE" -o "evidence/http/${ep}_$id.txt" -w "%{http_code}")
    echo "$id | $ep | $code"
  done
done
```

---

## 报告规范

每条越权 finding 必须：
- 明确身份 A、身份 B、目标资源 id
- 明确操作：读/写/删
- 明确影响：数据泄露 / 数据篡改 / 数据删除
- 明确是否可批量（若可批量，严重度 +1）
- CVSS：读越权 medium、写越权 high、删除越权 high-critical、提权 critical

---

## 完成标准

- [ ] 4+ 身份视角的 `/me` 全部验证
- [ ] 所有 API 端点 × 三态对照矩阵
- [ ] 批量接口专项测试
- [ ] 多租户隔离测试（若多租户）
- [ ] 幂等性测试
- [ ] 状态变更授权测试
- [ ] 所有 admin/system 接口的越权尝试
- [ ] 每条命中即 report_finding，报告含身份 A/B 与影响
