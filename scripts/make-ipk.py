"""Assemble the legacy single-app IPK layout and its installation scripts."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile

scratch, output = map(Path, sys.argv[1:3])
app = json.loads(Path("web/appinfo.json").read_text())
package_path = output / f'{app["id"]}_{app["version"]}_all.ipk'

package = scratch / "ipk"
package.mkdir(exist_ok=True)
data = package / "data"
control = package / "control"
for directory in (data, control):
    if directory.exists():
        shutil.rmtree(directory)
    directory.mkdir()
web = data / "usr/palm/applications" / app["id"]
shutil.copytree("web", web)
native = web / "native"
native.mkdir()
for cpu in ("arm", "i686"):
    binary = "palmcadence-" + cpu
    shutil.copy2(scratch / binary, native / binary)
shutil.copy2(scratch / "ca.pem", native / "ca.pem")
shutil.copy2("webos/palmcadence.upstart", native)
shutil.copytree("notices", native / "notices")
shutil.copy2(Path(scratch) / "go/LICENSE", native / "notices/Go.txt")
module_dir = subprocess.check_output(
    [str(scratch / "go/bin/go"), "list", "-m", "-f", "{{.Dir}}", "github.com/titanous/json5"], text=True,
).strip()
shutil.copy2(Path(module_dir) / "LICENSE", native / "notices/json5.txt")

for script in ("postinst", "prerm"):
    path = shutil.copy2(Path("webos") / script, control)
    Path(path).chmod(0o755)
installed_size = sum(p.stat().st_size for p in data.rglob("*") if p.is_file())
(control / "control").write_text(
    f'Package: {app["id"]}\nVersion: {app["version"]}\n'
    "Architecture: all\nSection: multimedia\nPriority: optional\n"
    f"Installed-Size: {(installed_size + 1023) // 1024}\n"
    "Maintainer: PalmCadence\nDescription: PalmCadence music player for Palm webOS\n"
)


def root_owner(item):
    item.uid = item.gid = 0
    item.uname = item.gname = "root"
    return item


for name, content in (("control", control), ("data", data)):
    with tarfile.open(package / f"{name}.tar.gz", "w:gz", format=tarfile.USTAR_FORMAT) as archive:
        for file in sorted(content.iterdir()):
            archive.add(file, arcname="./" + file.name, filter=root_owner)

(package / "debian-binary").write_text("2.0\n")
staged = package / "package.ipk"
staged.unlink(missing_ok=True)
subprocess.run(["ar", "rc", staged.name, "debian-binary", "control.tar.gz", "data.tar.gz"], cwd=package, check=True)
shutil.copy2(staged, package_path)
print(package_path)
