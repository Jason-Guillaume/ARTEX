---
name: security-tool-orchestration
description: 安全工具编排与检测。执行前探测 Playwright/Burp/ZAP/Nuclei/sqlmap 可用性，提供 curl/Playwright/Python 组合替代方案，管理 OAST 服务、HTTP 客户端、CVSS 计算器、速率限制与紧急停止。当其他 skill 需要调用工具时先看这里。
---
---
name: security-tool-orchestration
description: 安全工具编排。探测工具可用性、提供替代方案、管理 OAST、CVSS、速率限制、紧急停止。
---

# security-tool-orchestration — 工具编排

**职责**：给所有其他 skill 提供工具层能力；不重复造轮子；工具不可用时给出替代，**不虚构工具执行结果**。

---

## 前置：工具探测

每次会话启动时跑一次：

```bash
security_tools_probe  # 自定义工具（已注册）
# 或手工：
which curl python3 node jq openssl base64 dig nslookup host 2>&1
playwright --version 2>&1
which sqlmap nuclei zap.sh burpsuite msfvenom nikto 2>&1
```

结果存 `tools/probe.json`，后续 skill 引用。

---

## 工具矩阵与替代

| 工具 | 当前状态 | 用途 | 替代方案 |
|---|---|---|---|
| curl + jq | ✅ | 全 | — |
| Python 3.12 (urllib) | ✅ | HTTP、正则、base64、hashlib、hmac | requests 类库 |
| Node 20 + Playwright 1.64 | ✅ | DOM-XSS、动态请求、SSO 会话导入 | Selenium（若可用） |
| Playwright CLI | ✅ | 命令行浏览器测试 | 同上 |
| openssl | ✅ | TLS、证书、签名、JWT HMAC | — |
| base64 / md5sum / sha256sum | ✅ | 编码、校验 | — |
| dig / nslookup | ✅ | DNS | — |
| **OWASP ZAP** | ❌ 未安装 | 主动/被动扫描 | 手工 curl 请求矩阵 + 被动日志 |
| **Burp Suite** | ❌ 未安装 | 请求重放、扫描 | curl + Python 参数变异 |
| **Nuclei** | ❌ 未安装 | 已知 CVE 模板 | references/nuclei-like.md 精选路径 + curl |
| **sqlmap** | ❌ 未安装 | SQLi 自动化 | injection-security-audit 手册 payload |
| **msfvenom** | ❌ 未安装 | payload 生成 | Python 手写编码 |
| **interactsh / oastify** | 网络允许 | OOB 回连 | 自建 HTTP + DNS 回连 |

---

## 阶段 1：工具健康检查

```bash
curl --version | head -1
python3 --version
node --version
playwright --version
openssl version
```

写 `tools/probe.json`：

```json
{
  "curl": {"path":"/usr/bin/curl","version":"8.x"},
  "python": {"version":"3.12.15"},
  "node": {"version":"20.20.2"},
  "playwright": {"version":"1.64.1"},
  "zap": {"available":false},
  "burp": {"available":false},
  "nuclei": {"available":false},
  "sqlmap": {"available":false}
}
```

---

## 阶段 2：请求执行策略

### 2.1 速率限制（默认）

- 单目标 QPS ≤ 5
- 单并发连接数 ≤ 3
- 每 100 请求间隔 5 秒

```bash
# curl 组合限速
for ep in $(cat recon/api_merged.txt); do
  curl -sk "$ep" -o "evidence/http/$(echo $ep|md5sum|cut -c1-8).json"
  sleep 0.2
done
```

### 2.2 紧急停止

- 收到 5xx 连续 5 次 → 停 60 秒
- 收到 WAF 拦截特征 → 停 120 秒
- 目标无响应 → 停 300 秒

`poc/stop_signal.txt` 存在即中止（人工紧急停止）。

### 2.3 越界拦截

```bash
check_scope() {
  local url="$1"
  local host=$(echo "$url" | sed -E 's|https?://||;s|:.*||;s|/.*||')
  grep -qx "$host" auth/scope.md/domains.txt || {
    echo "⚠️ 越界：$host"; return 1
  }
  return 0
}
```

所有 skill 的 curl 前调用 `check_scope`。

---

## 阶段 3：Playwright 集成

### 3.1 会话导入（用户已授权）

**允许**：导入用户提供的 storage_state、cookies、localStorage。
**禁止**：自动登录、破解扫码、绕过 2FA。

```javascript
// scripts/import_session.js
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const context = await browser.newContext({
    storageState: 'auth/storage_state_user.json'  // 用户导入
  });
  const page = await context.newPage();
  page.on('request', r => console.log('REQ', r.url()));
  page.on('response', async r => {
    if (r.url().includes('/api/')) {
      console.log('API', r.url(), r.status());
    }
  });
  await page.goto('https://<target>/');
  await page.waitForTimeout(10000);
  await context.storageState({ path: 'evidence/session_after.json' });
  await browser.close();
})();
```

### 3.2 动态请求抓取

```javascript
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const apis = [];
  page.on('request', r => {
    if (r.url().match(/\/api\//)) apis.push({m:r.method(),u:r.url(),h:r.headers()});
  });
  page.on('response', async r => {
    if (r.url().match(/\/api\//)) {
      try { apis[apis.length-1].body = await r.text(); } catch(e){}
    }
  });
  // 遍历主要路由
  for (const p of ['/','/login','/dashboard','/admin']) {
    await page.goto(`https://<target>${p}`);
    await page.waitForTimeout(2000);
  }
  require('fs').writeFileSync('evidence/dynamic_apis.json', JSON.stringify(apis, null, 2));
  await browser.close();
})();
```

### 3.3 交互操作（点击、填写）

```javascript
await page.fill('#username', 'testuser');
await page.fill('#password', 'testpass');
await page.click('button[type=submit]');
await page.waitForURL('**/dashboard');
```

**限制**：只用已授权账号；不爆破密码；不绕过 2FA。

---

## 阶段 4：Burp / ZAP 替代方案

### 4.1 请求重放（替代 Burp Repeater）

```bash
# 保存请求到文件
curl -sk -X POST -H @auth/headers_user.txt -H "Content-Type: application/json" \
  -d @evidence/http/request_body.json \
  "$BASE/api/order/create" -o evidence/http/response.json -w "%{http_code}\n"

# 参数变异：脚本生成多个请求
python3 -c "
import json,subprocess
base = json.load(open('evidence/http/request_body.json'))
variants = []
for v in [base, {**base, 'amount':0}, {**base, 'amount':-1}, {**base, 'role':'admin'}]:
  variants.append(json.dumps(v))
for i,v in enumerate(variants):
  open(f'poc/variant_{i}.json','w').write(v)
"
for f in poc/variant_*.json; do
  curl -sk -X POST -H @auth/headers_user.txt -H "Content-Type: application/json" \
    -d @$f "$BASE/api/order/create" -o "evidence/http/resp_$(basename $f)" -w "%{http_code}\n"
done
```

### 4.2 主动扫描替代（ZAP 替代）

在 `references/nuclei-like.md` 提供精选的 CVE 探测路径：

```bash
# 例：Jenkins 未授权
curl -sk "$BASE/login?from=%2F" -o /dev/null -w "%{http_code}\n"
# 例：Spring actuator
curl -sk "$BASE/actuator/env" -o /dev/null -w "%{http_code}\n"
# 例：Druid
curl -sk "$BASE/druid/index.html" -o /dev/null -w "%{http_code}\n"
```

---

## 阶段 5：CVSS 计算器

自定义工具 `cvss_calc`：

```bash
cvss_calc --vector "AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:N"
# 输出：8.1 High (CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:N)
```

若工具不可用，用 [references/cvss.md](references/cvss.md) 手工打分表。

---

## 阶段 6：OOB 服务管理

### 6.1 oastify 创建

```bash
INSTANCE=$(curl -sS "https://api.oastify.com/public/v1/instance/create?name=pentest-$(date +%s)" | jq -r .instance)
echo "$INSTANCE" > poc/oob_domain.txt
```

### 6.2 自建回连服务器

```bash
python3 -c "
from http.server import BaseHTTPRequestHandler,HTTPServer
from datetime import datetime
class H(BaseHTTPRequestHandler):
  def do_GET(s):
    with open('evidence/http/callbacks.log','a') as f:
      f.write(f'{datetime.now()} {s.path}\n{s.headers}\n---\n')
    s.send_response(200); s.end_headers()
  def log_message(s,*a): pass
HTTPServer(('0.0.0.0',8088),H).serve_forever()
" &
echo $! > poc/callback_server.pid
```

### 6.3 DNS 回连

```bash
# 用 oastify 的 DNS 或自建 dnsmasq 监听
nslookup <oast-domain>
```

---

## 阶段 7：敏感信息处理

**报告脱敏**（生成前执行）：

```bash
# 找出报告中的 token/密码
grep -nE 'eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+' report/report.md
grep -nE '(password|passwd|secret|api_key|access_key)\s*[:=]' report/report.md -i
# 替换为 <REDACTED>
sed -i -E 's/eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/<REDACTED_JWT>/g' report/report.md
```

**audit.log 脱敏**：
- 每行操作日志，凭据/响应体脱敏后落日志

---

## 阶段 8：审计日志

每次工具调用自动写 `audit.log`：

```
[2026-10-22T09:05:12] tool=curl target=https://example.com/api/xxx status=200 size=1234
[2026-10-22T09:05:13] tool=playwright action=navigate url=...
[2026-10-22T09:05:14] tool=cvss_calc vector=AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:N result=8.1
```

---

## 阶段 9：异常处理

| 异常 | 处理 |
|---|---|
| 网络超时 | 重试 ≤ 3 次；超过后跳过并记录 |
| DNS 解析失败 | 停止并报错 |
| TLS 证书错误 | `-k` 忽略；记录到报告 |
| Token 失效 | 通知用户，不尝试爆破 |
| 目标 502/503 | 停 30 秒，避免打崩 |
| WAF 拦截 | 降速；不绕过 |
| OAST 服务不可达 | 用自建服务器 |
| Playwright 启动失败 | 使用 curl 替代；标记局限 |

---

## 阶段 10：清理

测试结束：
- 销毁 OAST 实例：`curl -X DELETE https://api.oastify.com/public/v1/instance/delete?session=...`
- 关闭自建回连服务器：`kill $(cat poc/callback_server.pid)`
- 清理临时文件：`rm -rf poc/temp_*`
- **不删除**：凭据、报告、证据、日志（保留供审计）

---

## 完成标准

- [ ] 工具探测结果落 `tools/probe.json`
- [ ] toolchain.md 列出实际使用的工具与版本
- [ ] 所有越界目标被拦截
- [ ] 敏感信息在报告和日志中脱敏
- [ ] 每条工具调用可回溯到 audit.log
- [ ] 结束时清理 OAST 与自建服务

---

## 与其他 skill 的接口

- **pentest-expert** 首次调用 security-tool-orchestration 做工具探测
- 所有审计 skill 通过本文档的封装调用工具，不直接裸 curl（除简单请求）
- 报告 skill 依赖 toolchain.md 与 audit.log
