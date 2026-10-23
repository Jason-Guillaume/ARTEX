# Nuclei-like 精选探测路径（无 Nuclei 时用）

以下路径字典用于发现已知组件的默认暴露点。**低速率、只 GET**，不修改、不上传。

## Spring Boot Actuator

```
/actuator
/actuator/env
/actuator/heapdump
/actuator/mappings
/actuator/beans
/actuator/configprops
/actuator/logfile
/actuator/threaddump
/actuator/health
```

## 常见 API 文档

```
/swagger
/swagger-ui.html
/swagger-ui/index.html
/swagger.json
/swagger/v2/swagger.json
/swagger/v3/api-docs
/api-docs
/openapi.json
/openapi.yaml
/docs
/redoc
/redocly
/api/v1/api-docs
/api/v1/swagger.json
/v2/api-docs
/v3/api-docs
```

## 常见后台 / 管理

```
/admin
/admin/
/admin/index.html
/login
/user/panel
/console
/dashboard
```

## 框架指纹（版本 banner）

- `Server: nginx/1.x`
- `X-Powered-By: PHP/8.x`
- `X-AspNet-Version: 4.0`
- `Server: Microsoft-IIS/10.0`
- `Server: Tomcat`
- `X-Powered-By: ASP.NET`
- `Server: Jetty/9.x`
- `Server: WebLogic`
- `Server: WebSphere`
- `Server: openresty/1.x`
- `X-Tungsten-Sync-Status: *`

## 常见中间件

```
/druid/index.html                # Druid 监控
/grafana                         # Grafana
/jenkins                         # Jenkins
/nacos                           # Nacos
/consul                          # Consul
/eureka                          # Eureka
/zipkin                          # Zipkin
/elasticsearch/_cat              # Elasticsearch
/redis-cli                       # Redis 暴露
/sonarqube                       # SonarQube
/ngrx                           # 一些监控
/prometheus                      # Prometheus
/telegraf                        # Telegraf
/kibana                          # Kibana
/datax                           # DataX
/nacos/v1/auth/login             # Nacos 弱口令
```

## 常见敏感文件

```
/.env
/.git/config
/.git/HEAD
/.svn/entries
/.hg/hgrc
/web.config
/.DS_Store
/sitemap.xml
/robots.txt
/phpinfo.php
/info.php
/backup.zip
/dump.sql
/.gitignore
/.htaccess
/.htpasswd
/WEB-INF/web.xml
/WEB-INF/classes
```

## 云与运维

```
/k8s                            # K8s API Server
/kubernetes                     # 同上
/etcd                           # etcd
/metrics                        # Prometheus metrics
/debug/pprof                   # Go pprof
/debug/vars                    # Node.js / Go debug
/actuator/heapdump              # 已列
/_internal/                     # Nginx internal
/status                         # 各种状态页
/server-status                  # Apache
/server-info                    # Apache
/nginx_status                   # Nginx
```

## 使用方式

```bash
for path in $(cat paths.txt); do
  code=$(curl -sk -o /dev/null -w "%{http_code} %{size_download}" --max-time 3 "$BASE$path")
  echo "$code $path"
done | sort -k1
```

**纪律**：
- 只做 GET
- QPS ≤ 3
- 单个目标 <500 请求
- 命中即停，不要继续扫
