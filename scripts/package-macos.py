#!/usr/bin/env python3
"""Create a real macOS menu bar bundle, including per-app languages."""
import pathlib,plistlib,sys
app=pathlib.Path(sys.argv[1]);version=sys.argv[2]
info={"CFBundleName":"TailBuddy","CFBundleDisplayName":"TailBuddy","CFBundleIdentifier":"io.github.imsingee.tailbuddy","CFBundleExecutable":"TailBuddy","CFBundlePackageType":"APPL","CFBundleShortVersionString":version,"CFBundleVersion":version,"CFBundleIconFile":"TailBuddy.icns","LSMinimumSystemVersion":"12.0","LSUIElement":True,"NSHighResolutionCapable":True,"CFBundleDevelopmentRegion":"en","CFBundleLocalizations":["en","zh-Hans"],"NSHumanReadableCopyright":"Copyright © 2026 TailBuddy contributors"}
with (app/'Contents/Info.plist').open('wb') as f:plistlib.dump(info,f)
for language in info['CFBundleLocalizations']:
 p=app/'Contents/Resources'/f'{language}.lproj';p.mkdir(parents=True,exist_ok=True)
 (p/'InfoPlist.strings').write_text('"CFBundleDisplayName" = "TailBuddy";\n',encoding='utf-8')
