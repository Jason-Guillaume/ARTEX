# 多身份三态对照快速矩阵

## 快速脚本

保存为 `poc/tri_state.sh`：

```bash
#!/bin/bash
BASE="https://<target>"
IDS=(A_1 A_2 B_1 B_2)   # 用户 A 的两个资源 ID，用户 B 的两个资源 ID

declare -A IDENTITIES=(
  [anon]=""
  [user_a]="auth/headers_user_a.txt"
  [user_b]="auth/headers_user_b.txt"
  [admin]="auth/headers_admin.txt"
)

test_ep() {
  local identity=$1
  local path=$2
  local method=${3:-GET}
  local hdr=${IDENTITIES[$identity]}
  local outfile="evidence/tri/${identity}_${path//\//_}.json"
  mkdir -p evidence/tri
  
  local args=("$BASE$path" -o "$outfile" -w "%{http_code} %{size_download}")
  [ -n "$hdr" ] && args=("-H" "@$hdr" "${args[@]}")
  [ "$method" != "GET" ] && args=("-X" "$method" "${args[@]}")
  
  curl -sk "${args[@]}"
}

# 常见端点
ENDPOINTS=(
  "GET /api/me"
  "GET /api/users/${IDS[0]}"
  "GET /api/users/${IDS[1]}"
  "GET /api/users/${IDS[2]}"
  "GET /api/users/${IDS[3]}"
  "POST /api/users"
  "DELETE /api/users/${IDS[2]}"
  "GET /api/admin/users"
  "GET /api/audit/log"
)

for ep in "${ENDPOINTS[@]}"; do
  method=$(echo $ep | awk '{print $1}')
  path=$(echo $ep | awk '{print $2}')
  for identity in anon user_a user_b admin; do
    echo "=== $identity $method $path ==="
    test_ep $identity $path $method
  done
done
```

## 分析表模板

| Endpoint | Anon | User A | User B | Admin | Verdict |
|---|---|---|---|---|---|
| GET /api/me | 401 | 200 | 200 | 200 | OK |
| GET /api/users/A_1 | 401 | 200 (own) | 403 | 200 | OK |
| GET /api/users/A_1 (as B) | - | - | 200 | - | **BOLA** |
| POST /api/users | 401 | 200 | 200 | 200 | OK |
| DELETE /api/users/B_1 (as A) | - | 200 | - | - | **BFLA** |
| GET /api/admin/users | 401 | 200 | 200 | 200 | **Critical** |

## 判定规则

| 组合 | 严重度 |
|---|---|
| Anon 200 | Critical (未授权) |
| User A 访问 User B 资源 200 | High (BOLA) |
| User A 调 admin 接口 200 | Critical (BFLA) |
| Admin 403 | High (配置错误) |
| User A 创建 User B 数据 200 | High (Mass Assignment) |

## 报告格式

每条 finding 引用矩阵中一行，附上证据文件路径。
