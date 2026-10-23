---
name: api-security-audit
description: API 接口安全测试，基于 OWASP API Security Top 10。支持导入 Swagger/OpenAPI/Postman/HAR/Burp 集合。BOLA/IDOR/BFLA、参数篡改、Mass Assignment、SQL/NoSQL 注入、异常响应泄露、幂等性、业务状态机。当任务目标是"系统化审计 API"时启用。
---
---
name: api-security-audit
description: API 安全测试。基于 OWASP API Security Top 10，覆盖 BOLA/BFLA/参数篡改/Mass Assignment/异常处理等。
---

# api-security-audit — API 接口安全测试

**依赖**：`api-recon`（前端 SPA 时的接口发现）；Swagger/OpenAPI/Postman/HAR 中任意一种（可选）。
**边界**：只做**接口层安全**，不覆盖 JS/前端。
**纪律**：每发现即 `report_finding`；不重复同一 (endpoint, param, vulnclass) 三元组。

---

## Phase 0 — 接口资产归集

| 输入 | 处理 |
|---|---|
| OpenAPI/Swagger JSON | `jq '.paths' openapi.json > recon/openapi_paths.json`，抽 method/path/params/response |
| Postman Collection | `jq '.item[]\|{n:.name,r:.request}' collection.json` |
| HAR | `jq '.log.entries[]\|{u:.request.url,m:.request.method,p:.request.postData}' *.har` |
| Burp 导出 | 解析 XML/CSV，抽 URL/method/headers |
| api-recon 输出 | `recon/api_merged.txt` 直接用 |

产出：`recon/api_inventory.json`，字段 `{method, path, params[], auth_required, tags[]}`。

---

## OWASP API Security Top 10 检查表

| # | 检查项 | 优先级 | 参考 |
|---|---|---|---|
| API1 BOLA | 对象级越权（IDOR） | P0 | Phase 1 |
| API2 BFLA | 功能级越权 | P0 | Phase 2 |
| API3 Property Manipulation | Mass Assignment | P0 | Phase 3 |
| API4 Unrestricted Resource Consumption | 无速率限制 | P1 | Phase 4 |
| API5 BPTB | Broken Function-Level Authz | P0 | Phase 2 |
| API6 Unrestricted Access to Sensitive BWAPI | 敏感字段回传 | P1 | Phase 5 |
| API7 SSRF | 服务端请求伪造 | P0 | 转 injection-security-audit |
| API8 API Security Misconfiguration | 版本暴露/CORS/认证缺陷 | P1 | Phase 6 |
| API9 Improper Assets Mgmt | 已知 CVE / 版本暴露 | P1 | Phase 7 |
| API10 Unsafe Consumption of APIs | 依赖信任 | P2 | Phase 8 |

---

## Phase 1 — BOLA / IDOR（对象级越权）

**方法论**：三态对照。**必须**至少两个身份。

### 1.1 找 ID 类资源

从 API 清单中筛路径含 `/{id}` `/{uid}` `/{uuid}` `/detail/{...}` `/get/{...}` 的接口。

### 1.2 三态对照矩阵

| 身份 | 请求目标 | 期望 |
|---|---|---|
| 匿名 | 任意 id | 401/403 |
| 用户 A | 自己的 id | 200 + 自己数据 |
| 用户 A | 用户 B 的 id | **403/404（若返回 200 = BOLA）** |
| 用户 B | 用户 A 的 id | **403/404（若返回 200 = BOLA）** |
| 管理员 | 任意 id | 200（合法） |

### 1.3 参数扫描（ID 变体）

```bash
UID_A=123
for variant in $UID_A $((UID_A+1)) $((UID_A+100)) 999999 0 -1 admin root; do
  curl -sk -H @auth/headers_user_a.txt "$BASE/api/user/$variant" -o "evidence/http/bola_$variant.json" -w "%{http_code} %{size_download}\n"
done
```

关注：响应长度、错误信息、是否返回了非本用户数据（对比字段：username/email/tenant_id）。

### 1.4 UUID / 时间戳 ID

- UUID：不可枚举 → 用 `recon/report-records` 之类列表接口先收集 UUID，再对非本用户 UUID 做 GET。
- 时间戳 ID：顺序递增，注意 `id+1`。

### 1.5 判定

- 用用户 A 的 token 请求用户 B 的资源，返回 200 + 用户 B 的数据 → **BOLA confirmed**。
- 返回 200 但内容为空 → 需进一步验证（可能是软删除，仍算 BOLA 变体）。
- 返回 404（不是 403）→ 可能是设计上的隐藏，但仍需交叉验证。

---

## Phase 2 — BFLA / BPTB（功能级越权）

**方法论**：找"管理员"或"高权限"接口，用普通用户 token 调。

### 2.1 识别

路径含 `admin`、`user/add`、`role/assign`、`permission/grant`、`system/config`、`audit/log` 等。

### 2.2 测试

```bash
# 用普通用户 token 调管理员接口
curl -sk -H @auth/headers_user.txt -X POST -H "Content-Type: application/json" -d '{}' "$BASE/api/admin/users" -o /dev/null -w "%{http_code}\n"
```

**判定**：
- 普通用户返回 200 + 创建成功 → **BFLA confirmed**（严重度 critical，可提权）。
- 返回 403 → OK。

---

## Phase 3 — Property Manipulation / Mass Assignment

**方法论**：给写操作 payload 添加额外字段（如 `role: "admin"`），看是否被服务端应用。

### 3.1 找写接口

从 api-recon 得所有 POST/PUT/PATCH。

### 3.2 变异字段

**高危字段名**（按领域）：
- 用户域：`role`、`is_admin`、`permissions`、`status`、`balance`、`credit`
- 订单域：`amount`、`price`、`discount`、`status`、`paid`
- 通用：`tenant_id`、`org_id`、`parent_id`、`created_at`、`updated_at`、`deleted`

### 3.3 测试

```bash
# 例：用户注册接口
curl -sk -X POST -H @auth/headers_user.txt -H "Content-Type: application/json" \
  -d '{"username":"test1","email":"t@t.com","role":"admin","is_admin":true}' \
  "$BASE/api/users" -o evidence/http/mass_1.json -w "%{http_code}\n"

# 用返回的 id 查详情，看 role/is_admin 是否被写入
curl -sk -H @auth/headers_user.txt "$BASE/api/users/<new_id>" | jq '.role,.is_admin'
```

**判定**：额外字段被服务端接受 → Mass Assignment confirmed。

---

## Phase 4 — Unrestricted Resource Consumption

**低影响测试**：**不做大压力测试**；只观察是否有速率限制迹象。

```bash
# 短时间发 10 个请求，看是否有 429
for i in $(seq 1 10); do
  curl -sk -o /dev/null -w "%{http_code}\n" "$BASE/api/slow-endpoint"
done
```

**判定**：
- 10 个请求都 200 且无 429 → 提示 "no rate limit observed"（info，不是漏洞）。
- 存在 429 → OK。
- 报告中标注，不宣称"可 DDoS"。

---

## Phase 5 — 过度数据返回（Sensitive Data Exposure）

**方法论**：拿 API 返回的响应，与"最小可用"对比。

```bash
# 用户列表 API
curl -sk -H @auth/headers_user.txt "$BASE/api/users?page=1" | jq '.data[0] | keys'
```

关注字段：
- 用户域：`password_hash`、`salt`、`email`、`phone`、`id_card`、`bank_card`、`token`、`api_key`、`session_id`
- 订单域：`payment_info`、`cvv`、`card_number`
- 系统域：`internal_id`、`source_ip`、`user_agent`、`last_login_ip`

**判定**：普通用户能看到不该看到的敏感字段 → Sensitive Data Exposure confirmed（medium-high）。

---

## Phase 6 — API Misconfiguration

### 6.1 CORS

参考 `web-security-audit` Phase 2，对每个 API 端点验证。

### 6.2 版本暴露

```bash
for p in /swagger /swagger-ui.html /api-docs /api/v1/swagger.json /docs /redoc /openapi.json /graphql /actuator /actuator/env /actuator/health /health /debug /metrics; do
  curl -sk -o /dev/null -w "%{http_code} $p\n" "$BASE$p"
done
```

### 6.3 错误响应

```bash
# 触发 400 / 500
curl -sk -X POST -H "Content-Type: application/json" -d '{bad_json' "$BASE/api/test" | head -50
```

关注：堆栈、SQL 报错、模板路径、内部 IP、类名。

### 6.4 认证缺陷

```bash
# 缺 token 能否访问受保护接口
curl -sk "$BASE/api/protected" -o /dev/null -w "%{http_code}\n"
```

401/403 → OK；200 → **critical**。

---

## Phase 7 — Improper Assets Management

- 已知框架版本 → 查 CVE（用 `security-tool-orchestration` 的 CVE 查询接口）。
- 中间件 banner（如 `Server: nginx/1.26.3`）→ 关联已知漏洞。
- 老版本 swagger、老 actuator、Druid 无鉴权等。

---

## Phase 8 — Unsafe Consumption of APIs

依赖第三方 API 的转发 / 代理，若目标直接信任第三方返回值 → 潜在风险。
- 检查是否有 `external_url` / `webhook` / `callback` 类参数（转 SSRF 测试）。

---

## 特殊类型测试

### 8.1 GraphQL

```bash
# introspection
curl -sk -X POST -H "Content-Type: application/json" \
  -d '{"query":"{__schema{types{name fields{name}}}}"}' \
  "$BASE/graphql" | jq | head -80
```

- introspection 200 + schema 完整 → 接口清单泄露（info/medium）。
- `id` 类字段尝试用其他用户 ID 替换 → BOLA。

### 8.2 gRPC-web / WebSocket

如接口返回 `application/grpc-web` 或页面连接 ws → 用 Playwright CDP 抓 WebSocket 帧。

### 8.3 OAuth / OpenID

若目标使用 OAuth：
- `/authorize` 的 `redirect_uri` 参数测试开放重定向。
- `/token` 用 refresh_token 反复试 → token 复用。
- `/userinfo` 是否需要 scope 校验。

### 8.4 文件上传（API 层）

- multipart 上传绕过类型校验（`.html`、`.svg`、双重扩展名）。
- 文件名路径穿越：`../../evil.php`。

---

## 特殊参数：Mass Assignment 快速清单

| 参数 | 领域 | 影响 |
|---|---|---|
| `role` / `roles[]` | 用户 | 提权 |
| `is_admin` / `isAdmin` | 用户 | 提权 |
| `permissions[]` | 用户 | 提权 |
| `balance` / `amount` / `price` | 交易 | 金额篡改 |
| `status` / `state` | 订单 | 状态机绕过 |
| `paid` / `is_paid` | 订单 | 免支付 |
| `discount` | 订单 | 折扣滥用 |
| `tenant_id` / `org_id` | 多租户 | 跨租户 |
| `deleted` / `is_deleted` | 通用 | 数据软删/未删 |

---

## 报告规范

每条 finding：
- OWASP API Top 10 分类
- 具体 endpoint + method + param
- baseline + exploit 请求/响应
- 影响（真实数据泄露 / 提权 / 篡改）
- 修复建议（服务端校验、参数白名单、权限边界、审计日志）

---

## 完成标准

- [ ] Phase 1 BOLA 至少 3 个 ID 类接口 × 3 态对照
- [ ] Phase 2 BFLA 覆盖所有 admin 接口
- [ ] Phase 3 Mass Assignment 覆盖所有 POST/PUT/PATCH
- [ ] Phase 4–7 至少抽样
- [ ] GraphQL / WebSocket 若存在必须测
- [ ] 报告含 OWASP 分类
