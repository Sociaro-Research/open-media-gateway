#!/usr/bin/env python3
"""Local browser window for the existing release runner; no third-party dependencies."""
import datetime
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parent
STATE = ROOT / 'window-v2.json'
ENV = dict(os.environ, PATH=str(Path.home()/'.docker/bin')+':/Applications/Docker.app/Contents/Resources/bin:/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
MODE_FLAGS = {'native': ['--check-only', '--native-only'], 'check': ['--check-only'],
              'deploy': [], 'native-deploy': ['--native-only'], 'deploy-only': ['--deploy-only'],
              'extended': ['--check-only', '--extended-only'], 'extended-deploy': ['--extended-only']}

HTML = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Gateway checks and deployment</title>
<style>body{font:16px -apple-system,BlinkMacSystemFont,sans-serif;background:#10151e;color:#f4f6fa;margin:0}main{max-width:850px;margin:48px auto;padding:24px}h1{font-size:28px}section{background:#1a2230;padding:24px;border-radius:16px;margin:20px 0}input{box-sizing:border-box;width:100%;padding:14px;border:1px solid #607086;border-radius:8px;background:#10151e;color:white;font:inherit;margin:12px 0}button{padding:13px 22px;border:0;border-radius:8px;background:#1765ff;color:white;font:inherit;cursor:pointer}.actions{display:flex;gap:12px;flex-wrap:wrap}button{min-height:44px}button:focus-visible{outline:2px solid white;outline-offset:3px}@media(max-width:480px){main{margin:12px auto;padding:12px}section{padding:16px}.actions button{width:100%}}button:disabled{opacity:.5;cursor:default}p{color:#bec9db;line-height:1.5}#status{font-size:23px;font-weight:600;color:white}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:380px;overflow:auto;font:13px monospace}progress{width:100%;height:10px;accent-color:#3880ff}#error{color:#ff9999}.success #status{color:#6ae2a1}.failed #status{color:#ff9999}small{color:#9cabc0}fieldset{border:0;padding:0;margin:0}legend{font-size:20px;margin-bottom:12px}.choice{display:flex;align-items:flex-start;gap:12px;min-height:44px;padding:10px 0;cursor:pointer}.choice input{width:22px;height:22px;margin:0;flex-shrink:0;accent-color:#3880ff}.choice small{display:block;margin-top:6px;line-height:1.5}input:focus-visible{outline:2px solid white;outline-offset:3px}</style>
<main><h1>Gateway checks and deployment</h1><section id="panel"><div id="status" role="status" aria-live="polite">Connecting…</div><p id="detail"></p><progress id="progress"></progress><small id="updated"></small><details><summary>Process log</summary><pre id="log"></pre></details></section>
<section><fieldset id="choices" disabled><legend>Select actions</legend>
<label class="choice"><input type="checkbox" id="native" checked><span>Local checks<small>Go, integration and browser tests</small></span></label>
<label class="choice"><input type="checkbox" id="full" checked><span>Additional CI checks<small>Security, Docker, test installation and Grafana</small></span></label>
<label class="choice"><input type="checkbox" id="deploy"><span>Deploy to server<small>Pushed main commit, backup and server checks</small></span></label>
</fieldset><p id="scope" aria-live="polite"></p><div class="actions"><button id="start" disabled>Run selected actions</button></div><p>To deploy without repeating CI, select only “Deploy to server”. Commit and push in GitHub Desktop first.</p><p>Closing this tab does not stop the process.</p><p id="error" role="alert"></p></section></main>
<script>
const base=location.pathname.replace(/\/$/,''),$=id=>document.getElementById(id);let busy=false,blocked=true;
function mode(){if($('deploy').checked)return $('full').checked?($('native').checked?'deploy':'extended-deploy'):$('native').checked?'native-deploy':'deploy-only';return $('full').checked?($('native').checked?'check':'extended'):$('native').checked?'native':null;}
function controls(){const m=mode();$('choices').disabled=blocked||busy;$('start').disabled=blocked||busy||!m;$('scope').textContent=m==='deploy-only'?'Deployment only. Local CI will be skipped.':m==='deploy'||m==='native-deploy'||m==='extended-deploy'?'Run the selected checks, then deploy.':m?'Run only the selected checks.':'Select at least one action.';}
async function poll(){try{const r=await fetch(base+'/status',{cache:'no-store'});if(!r.ok)throw Error();const s=await r.json();$('panel').className=s.state;$('status').textContent=s.title;$('detail').textContent=s.detail;$('log').textContent=s.log;$('updated').textContent='Updated: '+new Date().toLocaleTimeString();$('progress').hidden=s.state!=='running';blocked=s.state==='running';controls();if(!s.state||s.state==='idle')$('progress').hidden=true;}catch(e){$('status').textContent='Cannot reach the checks window';$('detail').textContent='Open the launcher again. The process may still be running.';blocked=true;controls();}}
async function start(mode){busy=true;controls();$('error').textContent='';try{const r=await fetch(base+'/start',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode})});const s=await r.json();if(!r.ok)throw Error(s.error);await poll();}catch(e){$('error').textContent=e.message}finally{busy=false;await poll()}}
$('full').onchange=controls;$('native').onchange=controls;$('deploy').onchange=controls;$('start').onclick=()=>{const m=mode();if(m)start(m);};poll();setInterval(poll,1000);
</script></html>'''

class Runner:
    def __init__(self):
        self.guard=threading.Lock()
        self.process=None
        self.log=None
        self.code=None
        self.error=''
        self.mode=None
        logs=sorted((p for p in (ROOT/'logs').glob('*.log') if p.name.startswith('window-v2-')),key=lambda p:p.stat().st_mtime)
        if logs:self.log=logs[-1]

    def status(self):
        with self.guard:
            text=self.log.read_text(errors='replace') if self.log and self.log.exists() else ''
            running=(self.process is not None and self.process.poll() is None) or (ROOT/'release.lock').exists()
            checking=self.mode in ('native','check','extended') or '[2/6] Local checks (no server access)' in text
            state='idle';title='Ready to check';detail='Select the checks and deployment actions to run.'
            if running:
                state='running';title='Checks running' if checking else 'Deployment running';detail='Preparing to start…'
                stages=[('[1/6]','1 of 6 · Checking connectivity'),('[2/6]','2 of 6 · Tests and code checks'),('[3/6]','3 of 6 · Checking version and SSH'),('[4/6]','4 of 6 · Uploading release to server'),('Backup:','5 of 6 · Creating backup'),('Building release','5 of 6 · Building and updating server'),('Migrations verified','6 of 6 · Checking the running site')]
                for marker,label in stages:
                    if marker in text:detail=label
                if detail.startswith('2 of 6'):
                    current=[line[7:] for line in text.splitlines() if line.startswith('CHECK: ')]
                    if current:detail+=' · '+current[-1][:200]
            elif self.error or self.code not in (None,0) or 'STOPPED:' in text:
                state='failed';title='Checks stopped' if checking else 'Deployment stopped';detail=self.error or 'An error occurred. See the log below.'
                if not self.error:
                    lines=text.splitlines()
                    stopped=[i for i,line in enumerate(lines) if line.startswith('STOPPED: ')]
                    if stopped:detail='\n'.join(lines[stopped[-1]:stopped[-1]+2])[:800]
            elif 'SUCCESS: deployed ' in text:
                state='success';title='Deployment completed';detail='The site is updated. Post-deployment checks passed.'
            elif 'SUCCESS: local checks passed' in text:
                state='success';title='Checks completed';detail='Selected local checks passed. The server was not changed.'
            elif self.log:
                state='failed';title='Completion not confirmed';detail='The process is no longer running, but success was not confirmed. Check the log.'
            return dict(state=state,title=title,detail=detail,log=text[-24000:])

    def start(self,mode):
        if mode not in MODE_FLAGS:raise ValueError('Unknown run mode.')
        with self.guard:
            if (self.process and self.process.poll() is None) or (ROOT/'release.lock').exists():raise ValueError('A process is already running.')
            stamp=datetime.datetime.now().strftime('%Y%m%d-%H%M%S')
            self.log=ROOT/'logs'/('window-v2-'+stamp+'-'+secrets.token_hex(3)+'.log')
            self.error='';self.code=None;self.mode=mode
            try:
                with self.log.open('w') as output:
                    self.process=subprocess.Popen([sys.executable,str(ROOT/'release.py'),'--yes', *MODE_FLAGS[mode]],cwd=ROOT,env=ENV,stdin=subprocess.DEVNULL,stdout=output,stderr=subprocess.STDOUT,start_new_session=True)
            except OSError as error:
                self.error=str(error);raise ValueError('Could not start the process: '+str(error))
            threading.Thread(target=self.finish,args=(self.process,),daemon=True).start()
    def finish(self,process):
        code=process.wait()
        with self.guard:
            if self.process is process:self.code=code


def serve(open_browser=True):
    os.umask(0o077)
    (ROOT/'logs').mkdir(exist_ok=True)
    # Reopening the launcher returns to the same status window.
    try:
        info=json.loads(STATE.read_text())
        with urlopen(info['url']+'/status',timeout=1) as response: json.load(response)
        if open_browser:subprocess.run(['/usr/bin/open',info['url']],check=True)
        return
    except (OSError,ValueError,KeyError):pass
    token=secrets.token_urlsafe(32);prefix='/'+token;runner=Runner()
    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def send(self,data,code=200,html=False):
            body=data.encode() if html else json.dumps(data).encode()
            self.send_response(code);self.send_header('Content-Type','text/html; charset=utf-8' if html else 'application/json');self.send_header('Cache-Control','no-store');self.send_header('X-Frame-Options','DENY');self.send_header('Referrer-Policy','no-referrer');self.end_headers();self.wfile.write(body)
        def do_GET(self):
            if self.path==prefix:self.send(HTML,html=True)
            elif self.path==prefix+'/status':self.send(runner.status())
            else:self.send({'error':'Not found'},404)
        def do_POST(self):
            origin='http://127.0.0.1:'+str(self.server.server_port)
            if self.path!=prefix+'/start' or self.headers.get('Origin')!=origin:self.send({'error':'Forbidden'},403);return
            try:
                size=int(self.headers.get('Content-Length','0'))
                if not 0<size<=8192:raise ValueError('Invalid request.')
                mode=json.loads(self.rfile.read(size)).get('mode')
                if mode not in MODE_FLAGS:raise ValueError('Unknown run mode.')
                runner.start(mode);self.send({'ok':True})
            except (ValueError,TypeError) as error:self.send({'error':str(error)},400)
    server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
    url='http://127.0.0.1:'+str(server.server_port)+prefix
    STATE.write_text(json.dumps({'url':url,'pid':os.getpid()}))
    if open_browser:subprocess.Popen(['/usr/bin/open',url],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    server.serve_forever()

def launch():
    os.umask(0o077)
    (ROOT/'logs').mkdir(exist_ok=True)
    def existing():
        try:
            info=json.loads(STATE.read_text())
            with urlopen(info['url']+'/status',timeout=1) as response: json.load(response)
            return info['url']
        except (OSError,ValueError,KeyError): return None
    url=existing()
    if not url:
        with (ROOT/'logs/window-server.log').open('a') as output:
            subprocess.Popen(['/usr/bin/python3',str(Path(__file__).resolve()),'--no-open'],stdin=subprocess.DEVNULL,stdout=output,stderr=output,start_new_session=True)
        for _ in range(50):
            url=existing()
            if url:break
            time.sleep(.1)
    if not url:raise RuntimeError('Could not open the status window. See logs/window-server.log.')
    if '--no-open' not in sys.argv:subprocess.run(['/usr/bin/open',url],check=True)

if __name__=='__main__':
    if '--launch' in sys.argv:launch()
    else:serve('--no-open' not in sys.argv)
