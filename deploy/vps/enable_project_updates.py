#!/usr/bin/env python3
"""安装更新入口；不下载或安装待升级的项目版本。"""
import json
import hashlib
import os
from pathlib import Path
import pwd
import re
import shutil
import subprocess
import sys


def engine_slot_name(engine):
    body = engine.read_bytes()
    match = re.search(rb"(?m)^UPDATER_VERSION = ([1-9][0-9]*)\r?$", body)
    if match is None:
        raise ValueError("更新引擎缺少版本声明")
    return f"engine-{int(match.group(1))}-{hashlib.sha256(body).hexdigest()[:12]}.py"


def enable(asset_root, version):
    root=Path(asset_root)
    def run(*args): subprocess.run(args,check=True,stdout=subprocess.DEVNULL)
    try: pwd.getpwnam("aimili-gateway-updater")
    except KeyError: run("useradd","--system","--no-create-home","--shell","/usr/sbin/nologin","aimili-gateway-updater")
    gateway=pwd.getpwnam("aimili-gateway"); updater=pwd.getpwnam("aimili-gateway-updater")
    public=Path("/var/lib/aimili-gateway/project-update")
    private=Path("/var/lib/aimili-gateway-project-update")
    for path,uid,gid,mode in [(public,0,gateway.pw_gid,0o750),(public/"requests",gateway.pw_uid,gateway.pw_gid,0o750),
        (public/"results",0,gateway.pw_gid,0o750),(public/"progress",0,gateway.pw_gid,0o750),
        (private,0,0,0o755),(private/"staging",updater.pw_uid,updater.pw_gid,0o700),(private/"transactions",0,0,0o700)]:
        path.mkdir(parents=True,exist_ok=True); os.chown(path,uid,gid); os.chmod(path,mode)
    lib=Path("/usr/local/lib/aimili-gateway"); lib.mkdir(parents=True,exist_ok=True); os.chmod(lib,0o755)
    slots=lib/"update-engines"; slots.mkdir(parents=True,exist_ok=True);os.chmod(slots,0o755)
    engine=root/"deploy/vps/project_update.py"; digest=hashlib.sha256(engine.read_bytes()).hexdigest()
    engine_name=engine_slot_name(engine)
    shutil.copyfile(engine,slots/engine_name);os.chmod(slots/engine_name,0o644)
    pointer=slots/"current.json"
    previous=None
    if pointer.exists():
        old=json.loads(pointer.read_text())
        previous=old.get("current")
        if previous and previous.get("sha256")==digest:
            previous=old.get("previous")
    next_pointer=dict(current=dict(file=engine_name,sha256=digest),previous=previous)
    temp=pointer.with_suffix(".tmp")
    with open(temp,"w",encoding="utf-8") as stream:
        os.chmod(temp,0o644);json.dump(next_pointer,stream);stream.flush();os.fsync(stream.fileno())
    os.replace(temp,pointer)
    shutil.copyfile(root/"deploy/vps/project_update_boot.py",lib/"project_update.py")
    os.chmod(lib/"project_update.py",0o644)
    shutil.copyfile(root/"deploy/vps/release-public.pem",lib/"release-public.pem")
    os.chmod(lib/"release-public.pem",0o644)
    (public/"current.json").write_text(json.dumps(dict(version=version)))
    os.chown(public/"current.json",0,gateway.pw_gid); os.chmod(public/"current.json",0o640)
    for name in ("fetch","install"):
        for suffix in ("service","timer"):
            file=f"aimili-project-update-{name}.{suffix}"
            shutil.copyfile(root/"deploy/systemd"/file,Path("/etc/systemd/system")/file)
            os.chmod(Path("/etc/systemd/system")/file,0o644)
    drop=Path("/etc/systemd/system/aimili-gateway.service.d/project-update.conf")
    drop.parent.mkdir(exist_ok=True)
    drop.write_text("[Service]\nReadOnlyPaths=/var/lib/aimili-gateway/project-update\nReadWritePaths=/var/lib/aimili-gateway/project-update/requests\n")
    os.chmod(drop,0o644)
    cfg=Path("/etc/aimili-gateway/config.json"); value=json.loads(cfg.read_text())
    value["projectUpdateEnabled"]=True
    # The caller must install a Gateway supporting this field before restarting it.
    cfg.write_text(json.dumps(value,separators=(",",":")))
    run("systemctl","daemon-reload")
    run("systemctl","enable","--now","aimili-project-update-fetch.timer","aimili-project-update-install.timer")


if __name__=="__main__": enable(Path(sys.argv[1]),sys.argv[2])
