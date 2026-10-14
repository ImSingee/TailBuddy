#!/usr/bin/env python3
"""Preserve the license/notice files of all dependency modules in the bundle."""
import json,pathlib,re,shutil,sys
out=pathlib.Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=True)
dec=json.JSONDecoder();modules={}
for file in sys.argv[2:]:
 s=pathlib.Path(file).read_text()
 while s.strip():
  s=s.lstrip();m,n=dec.raw_decode(s);s=s[n:]
  if m.get('Main') and m['Path'].startswith('github.com/ImSingee/'):continue
  modules[m['Path']]=m
manifest=[]
for path,m in sorted(modules.items()):
 d=pathlib.Path(m.get('Dir',''))
 if not m.get('Dir') or not d.is_dir():continue
 target=out/re.sub(r'[^A-Za-z0-9_.-]','_',path+'@'+m.get('Version','source'))
 count=0
 for f in d.rglob('*'):
  if f.is_file() and f.suffix.lower() not in {'.go','.py','.js','.ts','.c','.h','.cc','.cpp','.rs','.sh'} and re.match(r'^(licen[sc]e|copying|notice|copyright)([._-]|$)',f.name,re.I):
   dest=target/f.relative_to(d);dest.parent.mkdir(parents=True,exist_ok=True)
   if dest.exists():dest.unlink()
   shutil.copyfile(f,dest);count+=1
 manifest.append({'module':path,'version':m.get('Version','source'),'sum':m.get('Sum',''),'license_files':count})
(out/'modules.json').write_text(json.dumps(manifest,indent=2)+'\n')
