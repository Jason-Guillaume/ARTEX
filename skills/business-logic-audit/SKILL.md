---
name: business-logic-audit
description: 业务逻辑漏洞测试。状态机绕过、金额篡改、并发/竞态、重复提交、多步流程跳过、优惠券/邀请码滥用、退款异常、任务型状态回滚。当任务目标是"打业务流程"时启用。
---
---
name: business-logic-audit
description: 业务逻辑漏洞测试。状态机、并发竞态、重复提交、金额篡改、优惠券滥用、流程跳过。
---

# business-logic-audit — 业务逻辑审计

**核心思路**：业务规则不是自动安全的。攻击者通过观察 → 逆向 → 篡改 → 重复 → 并发。
**边界**：**不做破坏性操作**、**不刷单薅钱**、**不造成财务损失**。测试金额、券额度用最小值。

---

## Phase 0 — 业务流程建模

### 0.1 画状态机

对每个核心业务对象（订单、任务、工单、支付、审核、账户），画出：

```
[创建] → [待审核] → [进行中] → [已完成] → [已关闭]
              ↘ [已拒绝]
```

记录每个状态：
- 允许的下一状态
- 触发接口
- 需要的权限

### 0.2 找状态字段

从 API 响应中抽：`status`、`state`、`phase`、`stage`、`step`、`step_id`、`current_step`。

### 0.3 找触发接口

`api-recon` 输出的所有 `POST /xxx/advance` `/xxx/next` `/xxx/submit` `/xxx/approve` `/xxx/complete` `/xxx/cancel` `/xxx/refund` 等。

---

## Phase 1 — 状态机绕过

### 1.1 未完成任务直接完成

```bash
# 拿到一个刚创建的订单，状态=待审核
ORDER=$(curl -sk -H @auth/headers_user.txt -X POST -d '{}' "$BASE/api/orders" | jq -r '.id')
echo "订单 $ORDER 创建成功"
# 直接调"完成"
curl -sk -H @auth/headers_user.txt -X POST "$BASE/api/orders/$ORDER/complete" -o /dev/null -w "%{http_code}\n"
# 检查状态
curl -sk -H @auth/headers_user.txt "$BASE/api/orders/$ORDER" | jq '.status'
```

**判定**：状态跳到"已完成" → 状态机绕过 confirmed。

### 1.2 中间步骤跳过

- 多步骤表单：直接 POST 最后一步
- 多阶段审批：直接跳到"通过"
- 多步骤注册：跳过激活直接登录

### 1.3 状态回滚

- 完成后取消
- 已退款再退款
- 已拒绝再审核通过

```bash
# 已完成订单，尝试取消
curl -sk -H @auth/headers_user.txt -X POST "$BASE/api/orders/$ORDER/cancel" -o /dev/null -w "%{http_code}\n"
```

### 1.4 状态篡改（客户端）

- 前端提交 `status: "已完成"` 是否被服务端信任
- 前端隐藏按钮（如"删除"），直接调 API 是否仍生效

---

## Phase 2 — 金额与数量篡改

### 2.1 单价篡改

```bash
# 创建订单时指定单价
curl -sk -H @auth/headers_user.txt -X POST -H "Content-Type: application/json" \
  -d '{"product_id":123,"price":0.01,"quantity":1}' "$BASE/api/orders" | jq .
```

**判定**：`price` 被服务端接受 → 金额篡改 confirmed。

### 2.2 折扣 / 优惠券

- 折扣码：`discount_code`、`coupon_code`、`promo`
- 折扣金额：`discount`、`discount_amount`、`coupon_amount`
- 折扣比例：`discount_percent`

```bash
for code in FREE FREE100 100OFF ADMIN VIP 999999 000; do
  curl -sk -H @auth/headers_user.txt -X POST -H "Content-Type: application/json" \
    -d "{\"product_id\":1,\"discount_code\":\"$code\"}" "$BASE/api/orders" \
    -o "evidence/http/coupon_$code.json" -w "%{http_code}\n"
done
```

### 2.3 数量

- `quantity: -1`（负数退款）
- `quantity: 0`（0 元订单）
- `quantity: 999999`（超大量）
- `quantity: 1.5`（小数）
- `quantity: "1"`（字符串）

### 2.4 币种

- `currency: "JPY"` 但价格是 USD 数量
- `currency: "USD"` 但汇率未换算
- `currency: ""` 空币种

### 2.5 支付方式

- 绕过第三方支付：直接标 `paid: true`
- `payment_method: "free"`
- 修改支付凭证：`payment_id: ""` 但 `paid: true`

---

## Phase 3 — 重复提交 / 幂等性

### 3.1 重复创建

```bash
# 连发同一订单创建请求
for i in {1..5}; do
  curl -sk -H @auth/headers_user.txt -X POST -H "Content-Type: application/json" \
    -H "Idempotency-Key: fixed-key-123" \
    -d '{"product_id":1}' "$BASE/api/orders" -o "evidence/http/repeat_$i.json" -w "%{http_code}\n"
done
# 检查创建了几条
curl -sk -H @auth/headers_user.txt "$BASE/api/orders?page_size=10" | jq '.data | length'
```

**判定**：5 条订单 → 幂等性缺陷。

### 3.2 重复审核

```bash
# 管理员审核同一工单多次
for i in {1..3}; do
  curl -sk -H @auth/headers_admin.txt -X POST "$BASE/api/tickets/$ID/approve" -o /dev/null -w "%{http_code}\n"
done
```

**判定**：多次成功 → 可能触发重复奖励。

### 3.3 并发请求

```bash
# 并发修改同一资源
for i in {1..20}; do
  curl -sk -H @auth/headers_user.txt -X PUT -H "Content-Type: application/json" \
    -d "{\"amount\":$((100+i))}" "$BASE/api/accounts/1" &
done
wait
# 检查最终金额
curl -sk -H @auth/headers_user.txt "$BASE/api/accounts/1" | jq '.amount'
```

**判定**：最终金额不等于 20 次请求的最后一次（说明有丢失更新 / 无乐观锁）→ 竞态条件。

---

## Phase 4 — 并发 / 竞态条件

### 4.1 TOCTOU（Time of Check to Time of Use）

```bash
# 场景：先检查余额，再扣款
# 步骤 1：拿到 100 元余额
# 步骤 2：并发创建 3 个 50 元订单（应只有 1 个成功）
# 但服务器 TOCTOU 可能都成功
for i in {1..3}; do
  curl -sk -H @auth/headers_user.txt -X POST -d '{"amount":50}' "$BASE/api/withdraw" &
done
wait
# 检查余额
curl -sk -H @auth/headers_user.txt "$BASE/api/me" | jq '.balance'
```

**判定**：余额变负 → 并发竞态 confirmed（金额风险高）。

### 4.2 优惠券并发

- 优惠券限用 1 次
- 并发使用 N 次
- 服务器若用"先查后写" → 可能 N 次都通过

### 4.3 库存并发

- 库存 1
- 并发下单
- 若允许多单 → 超卖

---

## Phase 5 — 多租户与账户相关

### 5.1 邮箱验证

```bash
# 修改邮箱，触发验证邮件
curl -sk -H @auth/headers_user.txt -X POST -H "Content-Type: application/json" \
  -d '{"email":"attacker@evil.com"}' "$BASE/api/me"
# 尝试用新邮箱 + 默认密码登录
curl -sk -X POST -d "email=attacker@evil.com&p=<default>" "$BASE/login"
```

### 5.2 密码重置链接

- 是否绑定 user id：`/reset?token=xxx` vs `/reset?token=xxx&user=1`
- 是否一次性
- 是否过期
- 是否绑定原 email

### 5.3 邀请码

- 邀请码是否限用
- 邀请码是否可枚举（如 6 位数字 → 100 万种，可猜）
- 邀请码是否可重复邀请同一人

---

## Phase 6 — 权限变更后的即时性

- 管理员 A 修改用户 B 的角色
- 用户 B 立即用**旧 token** 访问被撤销的接口
- 若仍可用 → 权限变更未即时生效（结合 auth-session-audit Phase 3.3）

---

## Phase 7 — 常见业务风险清单

| 业务 | 风险 | 严重度 |
|---|---|---|
| 支付 | 金额篡改、重复支付、绕过支付 | critical |
| 优惠券 | 无限使用、跨账户使用、金额负数 | high |
| 库存 | 超卖 | high |
| 审核流程 | 跳过审批、拒绝后重审 | medium-high |
| 账户 | 邮箱验证绕过、密码重置链接泄露 | high |
| 邀请码 | 无限邀请、跨账户 | medium |
| 退款 | 重复退款、超额退款 | high |
| 提现 | 负数提现、并发提现 | high |
| 权限 | 修改后未即时生效 | medium |
| 数据 | 状态回滚、软删绕过 | medium |

---

## 完成标准

- [ ] 至少 3 个核心业务对象建模
- [ ] 状态机绕过测试 ≥ 5 个状态转换
- [ ] 金额/数量/折扣至少测 10 种变体
- [ ] 幂等性测试（Idempotency-Key + 重复请求）
- [ ] 并发竞态测试（≥ 10 并发）
- [ ] 邮箱/密码/邀请码等账户相关流程
- [ ] 每条命中即 report_finding；影响必须量化（金额 / 次数 / 数据量）

---

## 报告规范

业务逻辑 finding 必须：
- 明确业务流程图（状态机）
- 明确攻击步骤（可复现）
- 明确影响量化：多花/少付多少钱、多拿/多退多少次
- 修复建议：服务端状态校验、Idempotency-Key、乐观锁（version 字段）、金额服务端生成、并发锁
