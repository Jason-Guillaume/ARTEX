#!/usr/bin/env python3
"""DOM-XSS 静态扫描器：分析 JS bundle 中的 sink/source 组合。

用法：python3 js-xss-scan.py <bundle_dir> <out_json>
输出：{file: {pattern: [contexts]}}
"""
import os, re, json, sys

SINK_PATTERNS = {
    # HTML sinks
    'innerHTML_assign': r'\.innerHTML\s*=\s*[^;\n]+',
    'outerHTML_assign': r'\.outerHTML\s*=\s*[^;\n]+',
    'insertAdjacentHTML': r'insertAdjacentHTML\s*\(\s*["\'][^"\']+["\']\s*,\s*[^;\n]+',
    'document_write': r'document\.write(?:ln)?\s*\([^)]{0,300}',
    # React
    'dangerouslySetInnerHTML': r'dangerouslySetInnerHTML\s*:\s*\{',
    # JS eval
    'eval_call': r'\beval\s*\(',
    'new_Function': r'new\s+Function\s*\(',
    'setTimeout_string': r'setTimeout\s*\(\s*["\'`]',
    'setInterval_string': r'setInterval\s*\(\s*["\'`]',
    'setTimeout_template': r'setTimeout\(\s*`[^`]*\$\{',
    # Angular
    'ng_binding': r'\{\{[^}]*\}\}',
    # jQuery
    'jQuery_html': r'\$\(.*\)\.html\s*\(',
    'jQuery_attr_html': r'\$\(.*\)\.attr\s*\(\s*["\']html',
}

SOURCE_PATTERNS = {
    # URL / 路由参数
    'location_hash': r'location\.hash',
    'location_search': r'location\.search',
    'location_href': r'location\.href',
    'location_pathname': r'location\.pathname',
    'document_url': r'document\.URL\s*\||\s*document\.location',
    'URLSearchParams': r'new\s+URLSearchParams\s*\(',
    'atob': r'\batob\s*\(',
    # 消息
    'postMessage_recv': r'\.onmessage\s*=\s*|addEventListener\s*\(\s*["\']message',
    'postMessage_no_origin': r'event\.data[^.]*\.(innerHTML|outerHTML|insertAdjacentHTML|setAttribute|src|href|textContent)',
    # 响应注入
    'response_json_innerHTML': r'await.*\.json\(\)[\s\S]{0,300}\.innerHTML',
    'response_text_innerHTML': r'await.*\.text\(\)[\s\S]{0,300}\.innerHTML',
    # 前端路由
    'react_useParams': r'useParams\(\)[\s\S]{0,150}\.innerHTML',
    'vue_route_params': r'\$route\.params[\s\S]{0,150}\.innerHTML',
    # 模板拼接
    'template_literal_innerHTML': r'`[^`]*\$\{[^}]*\}[^`]*`\s*[,;)]',
    'concatenate_innerHTML': r'\+\s*[^+\n]+\s*\+\s*\n?\s*(?:\.innerHTML)',
}

CTX_WINDOW = 120


def scan_file(path):
    try:
        with open(path, encoding='utf-8', errors='ignore') as f:
            src = f.read()
    except Exception:
        return {}
    result = {}
    for name, pat in SINK_PATTERNS.items():
        matches = []
        for m in re.finditer(pat, src):
            start = max(0, m.start() - CTX_WINDOW)
            end = min(len(src), m.end() + CTX_WINDOW)
            matches.append(src[start:end].replace('\n', ' ')[:250])
        if matches:
            result[name] = matches[:5]
    for name, pat in SOURCE_PATTERNS.items():
        matches = []
        for m in re.finditer(pat, src):
            start = max(0, m.start() - CTX_WINDOW)
            end = min(len(src), m.end() + CTX_WINDOW)
            matches.append(src[start:end].replace('\n', ' ')[:250])
        if matches:
            result[name] = matches[:5]
    return result


def main():
    if len(sys.argv) < 3:
        print(__doc__); sys.exit(1)
    bundle_dir, out = sys.argv[1], sys.argv[2]
    all_results = {}
    for f in sorted(os.listdir(bundle_dir)):
        if f.endswith('.js'):
            r = scan_file(os.path.join(bundle_dir, f))
            if r:
                all_results[f] = r
    with open(out, 'w', encoding='utf-8') as fp:
        json.dump(all_results, fp, ensure_ascii=False, indent=2)
    # 摘要
    total_sinks = sum(len(v) for d in all_results.values() for k,v in d.items() if k in SINK_PATTERNS)
    total_sources = sum(len(v) for d in all_results.values() for k,v in d.items() if k in SOURCE_PATTERNS)
    print(f"Scanned {len(all_results)} files with hits; total sink hits={total_sinks}, source hits={total_sources}")
    print(f"Output: {out}")


if __name__ == '__main__':
    main()
