#!/usr/bin/env python3
"""Exercise packaged native UI and argument-domain language overrides, offline."""
import pathlib,subprocess,sys
app=pathlib.Path(sys.argv[1]).resolve()
exe=app/'Contents/MacOS/TailBuddy'
for language in ('en','zh-Hans'):
    # Apple's per-process NSArgumentDomain overrides AppleLanguages without
    # writing global or per-app user preferences on the runner.
    r=subprocess.run([str(exe),'--print-locale','-AppleLanguages',f'({language})'],capture_output=True,text=True,check=True,timeout=20)
    assert r.stdout.strip()==language,(language,r.stdout,r.stderr)
    print(f'Bundle localization {language}: passed')
# Smoke mode explicitly disables automatic reconnection, even with saved intent.
# It opens the native window + tray, draws, then quits after two seconds.
subprocess.run([str(exe),'--smoke-test'],check=True,timeout=30)
print('Native menu bar/window startup and clean exit: passed; no tailnet joined')
