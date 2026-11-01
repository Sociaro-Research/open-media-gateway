#!/usr/bin/env python3
"""Enforce the design-system prohibition on manual view-refresh controls."""
import argparse
from html.parser import HTMLParser
from pathlib import Path
import re


class Controls(HTMLParser):
    def __init__(self):
        super().__init__()
        self.stack = []
        self.labels = []

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        control = tag in ('button', 'input', 'a') or values.get('role') == 'button'
        if control:
            self.labels.extend(values.get(name, '') for name in ('aria-label', 'title', 'value'))
        if tag not in ('input', 'img', 'br', 'hr', 'meta', 'link', 'source', 'wbr'):
            self.stack.append((tag, control))

    def handle_endtag(self, tag):
        for index in range(len(self.stack) - 1, -1, -1):
            if self.stack[index][0] == tag:
                del self.stack[index:]
                break

    def handle_data(self, data):
        if any(control for _, control in self.stack):
            self.labels.append(data.strip())


def action_call(source, label_start):
    """Bound the one documented exception to its own action callback."""
    prefix = source[:label_start]
    match = re.search(r'button\(\s*$', prefix)
    if not match:
        return ''
    start = match.start()
    depth, quote, escaped = 0, '', False
    for index in range(start + len('button'), len(source)):
        char = source[index]
        if quote:
            if escaped:
                escaped = False
            elif char == '\\':
                escaped = True
            elif char == quote:
                quote = ''
        elif char in "'\"`":
            quote = char
        elif char == '(':
            depth += 1
        elif char == ')':
            depth -= 1
            if depth == 0:
                return source[start:index + 1]
    return ''


def violations(root):
    failures = []
    literal = re.compile(r'''(['"`])((?:Refresh|Reload)\b[^'"`\n]*)\1''', re.I)
    for path in sorted(root.rglob('*')):
        if path.suffix not in ('.js', '.html'):
            continue
        source = path.read_text()
        parser = Controls()
        parser.feed(source)
        labels = [(label, -1) for label in parser.labels]
        if path.suffix == '.js':
            labels += [(m.group(2), m.start()) for m in literal.finditer(source)]
        for label, position in labels:
            if not re.match(r'^(refresh|reload)\b', label, re.I):
                continue
            # This documented action reconciles Stripe state; it is a write,
            # never a view read and must never be invoked by background polling.
            if path.name == 'payments.js' and label == 'Refresh invoice' and "/refresh',{method:'POST'}" in action_call(source, position):
                continue
            if path.name == 'docs.js' and label == 'Refresh the signed URL, or send browser mutations from the same origin.':
                continue  # API URL renewal guidance, not a view-refresh control.
            failures.append(f'{path.relative_to(root)}: forbidden manual control {label!r}')
    return failures


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1] / 'web/public/ui')
    args = parser.parse_args()
    failures = violations(args.root)
    if failures:
        raise SystemExit('\n'.join(failures))
    print('UI automatic-update control policy passed')
