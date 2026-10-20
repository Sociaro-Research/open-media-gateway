#!/usr/bin/env python3
"""Include runtime dependency notices in the scratch application image."""
import json
from pathlib import Path
import shutil
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
destination = Path(sys.argv[1])
destination.mkdir(parents=True, exist_ok=True)
data = subprocess.check_output(["go", "list", "-deps", "-json", "./cmd/gateway", "./cmd/worker", "./cmd/webhook-receiver"], text=True)
decoder, modules = json.JSONDecoder(), {}
while data.strip():
    item, end = decoder.raw_decode(data.lstrip())
    data = data.lstrip()[end:]
    module = item.get("Module")
    if module and not module.get("Main"):
        modules[module["Path"]] = module.get("Replace", module)
index = []
for name, module in sorted(modules.items()):
    files = [p for p in Path(module["Dir"]).iterdir() if p.is_file() and p.name.lower().startswith(("license", "copying", "notice"))]
    if not files and name == "github.com/byteplus-sdk/byteplus-sdk-golang" and module["Version"] == "v1.0.16":
        files = [root / "deploy/licenses/byteplus-sdk-golang-LICENSE"]
    if not files:
        raise RuntimeError("Review and include missing dependency license: " + name)
    directory = destination / (name + "@" + module["Version"])
    directory.mkdir(parents=True, exist_ok=True)
    for file in files:
        shutil.copyfile(file, directory / file.name)
    index.append({"module": name, "version": module["Version"], "notices": [p.name for p in files]})
goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
go_license = goroot / "LICENSE"
if not go_license.exists():
    go_license = goroot.parent / "LICENSE"  # Homebrew keeps notices above libexec.
shutil.copyfile(go_license, destination / "Go-LICENSE")
shutil.copyfile(root / "internal/payments/invoicefonts/LICENSE", destination / "LiberationSans-LICENSE")
shutil.copyfile(root / "deploy/licenses/README.md", destination / "README.md")
(destination / "modules.json").write_text(json.dumps(index, indent=2) + "\n")
