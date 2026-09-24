#!/usr/bin/env python3
"""完整项目更新：无权限联网下载，root 离线验签、备份、安装和恢复。"""
import datetime as dt
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import socket
import sqlite3
import stat
import subprocess
import sys
import tarfile
import time
import urllib.request

PUBLIC = Path("/var/lib/aimili-gateway/project-update")
PRIVATE = Path("/var/lib/aimili-gateway-project-update")
STAGING = PRIVATE / "staging"
KEY = Path("/usr/local/lib/aimili-gateway/release-public.pem")
ORIGIN = "https://github.com/thzyh/aimili-gateway/releases/download/"
RELEASES = "https://api.github.com/repos/thzyh/aimili-gateway/releases?per_page=100"
CONFIG = Path("/etc/aimili-gateway/config.json")
EGRESS = Path("/var/lib/aimili-gateway/aimili-egress")
GATEWAY_DB = Path("/var/lib/aimili-gateway/aimili-gateway.db")
XUI_DB = Path("/etc/x-ui/x-ui.db")
XUI_BINARY = Path("/usr/local/x-ui/x-ui")
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-vps\Z")
HEX = re.compile(r"[a-f0-9]{64}\Z")
MAX_PACKAGE = 256 * 1024 * 1024


class UpdateError(Exception):
    pass


@contextmanager
def database(path):
    connection=sqlite3.connect(path)
    try:
        with connection: yield connection
    finally: connection.close()


def command(args, timeout=90):
    result = subprocess.run(args, capture_output=True, timeout=timeout)
    if result.returncode:
        raise UpdateError("command_failed")
    return result.stdout


def atomic_json(path, value, mode=0o640):
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(".tmp")
    with open(temp, "w", encoding="utf-8") as stream:
        os.chmod(temp, mode)
        json.dump(value, stream, separators=(",", ":"))
        stream.flush(); os.fsync(stream.fileno())
    if os.name != "nt" and os.geteuid() == 0 and path.is_relative_to(PUBLIC):
        import grp
        os.chown(temp, 0, grp.getgrnam("aimili-gateway").gr_gid)
    os.replace(temp, path)
    if os.name != "nt":
        fd=os.open(path.parent, os.O_DIRECTORY); os.fsync(fd); os.close(fd)


def read_file(path, limit=65536):
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, "rb") as stream:
        info=os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit or info.st_nlink != 1:
            raise UpdateError("invalid_payload")
        body=stream.read(limit+1)
        if len(body)>limit: raise UpdateError("invalid_payload")
        return body


def read_json(path):
    return json.loads(read_file(path))


def copy_untrusted(source,target,limit):
    fd=os.open(source,os.O_RDONLY|getattr(os,"O_NOFOLLOW",0))
    with os.fdopen(fd,"rb") as stream, open(target,"wb") as output:
        info=os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size>limit or info.st_nlink!=1: raise UpdateError("invalid_payload")
        total=0
        for chunk in iter(lambda:stream.read(1024*1024),b""):
            total+=len(chunk)
            if total>limit: raise UpdateError("invalid_payload")
            output.write(chunk)
        output.flush(); os.fsync(output.fileno())


def validate_request(r):
    if not isinstance(r,dict) or not HEX.fullmatch(r.get("runId", "")) or r.get("kind") != "project" or r.get("dryRun",False):
        raise UpdateError("invalid_request")
    if r.get("action") == "check" and not r.get("version"): return r
    if r.get("action") == "apply" and VERSION.fullmatch(r.get("version", "")): return r
    raise UpdateError("invalid_request")


def version_tuple(version):
    match=VERSION.fullmatch(version)
    if not match: raise UpdateError("version_invalid")
    return tuple(int(x) for x in match.groups())


def is_newer(target, current):
    return bool(current.startswith("legacy-")) or (bool(VERSION.fullmatch(current)) and version_tuple(target)>version_tuple(current))


def progress(r, state, percent, phase, staging=None):
    value=dict(runId=r["runId"], kind="project", version=r.get("version",""), action=r["action"], state=state, percent=percent, phase=phase)
    path=staging/"progress.json" if staging else PUBLIC/"progress"/(r["runId"]+".json")
    atomic_json(path,value)


def finish(r,state,error=""):
    value=dict(runId=r["runId"],kind="project",version=r.get("version",""),state=state,finishedAt=dt.datetime.now(dt.timezone.utc).isoformat())
    if error: value["errorCode"]=error
    atomic_json(PUBLIC/"results"/(r["runId"]+".json"),value)


def download(url,path,limit,report=None):
    request=urllib.request.Request(url,headers={"User-Agent":"Aimili-Gateway-Updater","Accept":"application/vnd.github+json"})
    try:
        with urllib.request.urlopen(request,timeout=45) as response, open(path,"wb") as stream:
            total=int(response.headers.get("Content-Length",0)); received=0; deadline=time.monotonic()+900
            if total>limit: raise UpdateError("invalid_payload")
            while True:
                chunk=response.read(1024*1024)
                if not chunk: break
                received+=len(chunk)
                if received>limit or time.monotonic()>deadline: raise UpdateError("download_failed")
                stream.write(chunk)
                if report: report(received,total)
            stream.flush(); os.fsync(stream.fileno())
            if total and received!=total: raise UpdateError("download_failed")
    except UpdateError: raise
    except Exception: raise UpdateError("download_failed") from None


def verify_manifest(directory,tag):
    try:
        command(["openssl","pkeyutl","-verify","-pubin","-inkey",str(KEY),"-rawin","-in",str(directory/"manifest.json"),"-sigfile",str(directory/"manifest.sig")])
    except Exception: raise UpdateError("invalid_signature") from None
    manifest=read_json(directory/"manifest.json")
    if manifest.get("schemaVersion")!=1 or manifest.get("release")!=tag or not VERSION.fullmatch(tag): raise UpdateError("invalid_payload")
    if manifest.get("updateContract")!=1: raise UpdateError("unsupported_release")
    if not re.fullmatch(r"[a-f0-9]{40}",manifest.get("gatewayCommit","")): raise UpdateError("invalid_payload")
    for key,name in (("package","aimili-vps-package.tar.gz"),("xui","x-ui-custom-linux-amd64")):
        asset=manifest.get("assets",{}).get(key,{})
        if asset.get("name")!=name or not HEX.fullmatch(asset.get("sha256","")): raise UpdateError("invalid_payload")
    if not VERSION.fullmatch(manifest["assets"]["xui"].get("release",tag)): raise UpdateError("invalid_payload")
    return manifest


def sha(path):
    digest=hashlib.sha256()
    with open(path,"rb") as stream:
        for chunk in iter(lambda:stream.read(1024*1024),b""): digest.update(chunk)
    return digest.hexdigest()


def fetch(r):
    stage=STAGING/r["runId"]; stage.mkdir(mode=0o700,parents=True,exist_ok=True)
    if (stage/"ready.json").exists(): return
    try:
        progress(r,"downloading",2,"discover" if r["action"]=="check" else "download",stage)
        if r["action"]=="check":
            download(RELEASES,stage/"releases.json",4*1024*1024)
            releases=json.loads((stage/"releases.json").read_bytes())
            candidates=[x["tag_name"] for x in releases if not x.get("draft") and not x.get("prerelease") and VERSION.fullmatch(x.get("tag_name",""))]
            if not candidates: raise UpdateError("release_missing")
            tag=max(candidates,key=version_tuple)
        else: tag=r["version"]
        for name in ("manifest.json","manifest.sig"):
            download(ORIGIN+tag+"/"+name,stage/name,65536)
        manifest=verify_manifest(stage,tag)
        if r["action"]=="apply":
            for key in ("package","xui"):
                asset=manifest["assets"][key]; name=asset["name"]
                release=asset.get("release",tag)
                download(ORIGIN+release+"/"+name,stage/name,MAX_PACKAGE,
                         lambda done,total: progress(r,"downloading",10+int(35*done/total) if total else 10,"download",stage))
                if sha(stage/name)!=asset["sha256"]: raise UpdateError("invalid_payload")
        atomic_json(stage/"ready.json",dict(version=tag))
    except Exception as error:
        code=str(error) if isinstance(error,UpdateError) else "download_failed"
        atomic_json(stage/"ready.json",dict(error=code))


def extract_package(archive,destination):
    destination.mkdir(parents=True,exist_ok=True)
    with tarfile.open(archive,"r:gz") as source:
        seen=set(); total=0
        for item in source:
            path=PurePosixPath(item.name)
            if path.is_absolute() or ".." in path.parts or not (item.isfile() or item.isdir()) or path in seen:
                raise UpdateError("invalid_payload")
            seen.add(path); total+=item.size
            if total>MAX_PACKAGE*3: raise UpdateError("invalid_payload")
            target=destination/path
            if item.isdir(): target.mkdir(parents=True,exist_ok=True)
            else:
                target.parent.mkdir(parents=True,exist_ok=True)
                with source.extractfile(item) as stream, open(target,"xb") as output: shutil.copyfileobj(stream,output)


def install_plan(package):
    pairs=[(package/"bin"/name,Path("/usr/local/bin")/name) for name in ("aimili-gateway","aimili-gateway-admin")]
    pairs += [(p,Path("/opt/aimili-gateway/services/aimili-egress")/p.name) for p in sorted((package/"services/aimili-egress").glob("*.py"))]
    pairs += [(package/"deploy/bin/aimili-gateway-account",Path("/usr/local/sbin/aimili-gateway-account")),
              (package/"scripts/aimili_xui_protocol_transaction.py",Path("/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py")),
              (package/"deploy/vps/project_update.py",Path("/usr/local/lib/aimili-gateway/project_update.py"))]
    if not (package/"services/aimili-egress/vpngate_manager.py").is_file() or any(not src.is_file() for src,_ in pairs): raise UpdateError("invalid_payload")
    return pairs


def identities():
    output={}
    with database(GATEWAY_DB) as db:
        for table,columns in {
            "gateway_subscription":"id,resource_name,client_id,subscription_id",
            "main_egress":"id,resource_name,public_inbound_id,mixed_inbound_id,public_port,mixed_port,enabled",
            "proxy_groups":"id,resource_name,aimili_slot,public_inbound_id,mixed_inbound_id,public_port,mixed_port",
            "aggregate_config":"id,resource_name,vless_inbound_id,vless_port,enabled",
        }.items(): output[table]=db.execute("select "+columns+" from "+table+" order by id").fetchall()
    with database(XUI_DB) as db:
        output["inbounds"]=db.execute("select id,port,protocol,settings,stream_settings from inbounds order by id").fetchall()
    return hashlib.sha256(json.dumps(output,sort_keys=True).encode()).hexdigest()


def quick_check():
    for path in (GATEWAY_DB,XUI_DB):
        with database(path) as db:
            if db.execute("pragma quick_check").fetchone()!=("ok",): raise UpdateError("database_invalid")


def check_layout():
    cfg=read_json(CONFIG)
    if cfg.get("databasePath")!=str(GATEWAY_DB) or not EGRESS.is_dir(): raise UpdateError("unsupported_layout")
    unit=command(["systemctl","show","aimilivpn","-p","ExecStart","--value"]).decode()
    if "/opt/aimili-gateway/services/aimili-egress/vpngate_manager.py" not in unit: raise UpdateError("unsupported_layout")
    for service in ("aimili-gateway","aimilivpn","x-ui","caddy"): command(["systemctl","is-active","--quiet",service])
    quick_check()


def copy_atomic(source,target):
    target.parent.mkdir(parents=True,exist_ok=True)
    temp=target.with_name(target.name+".project-update")
    shutil.copy2(source,temp)
    if os.name!="nt":
        info=source.stat(); os.chown(temp,info.st_uid,info.st_gid)
    with open(temp,"r+b") as stream: os.fsync(stream.fileno())
    os.replace(temp,target)


def stop_services():
    command(["systemctl","stop","aimili-gateway","aimilivpn","x-ui"],180)


def start_services():
    command(["systemctl","start","x-ui","aimilivpn","aimili-gateway"],180)


def healthy():
    for service in ("aimili-gateway","aimilivpn","x-ui","caddy"): command(["systemctl","is-active","--quiet",service])
    with urllib.request.urlopen("http://127.0.0.1:9080/healthz",timeout=5) as response:
        if response.status!=200: raise UpdateError("health_failed")
    with socket.create_connection(("127.0.0.1",8790),timeout=5): pass
    with database(XUI_DB) as db:
        for port, in db.execute("select port from inbounds where enable=1"):
            with socket.create_connection(("127.0.0.1",port),timeout=3): pass


def wait_health():
    deadline=time.monotonic()+120
    while True:
        try: healthy(); return
        except Exception:
            if time.monotonic()>deadline: raise UpdateError("health_failed") from None
            time.sleep(3)


def rollback(journal):
    stop_services()
    for entry in journal["files"]:
        target=Path(entry["target"])
        if entry["backup"]: copy_atomic(Path(entry["backup"]),target)
        elif target.exists(): target.unlink()
    for path in (GATEWAY_DB,XUI_DB):
        for suffix in ("-wal","-shm"):
            side=Path(str(path)+suffix)
            if side.exists(): side.unlink()
    # Egress state is private; restore only the transaction's known snapshot.
    for src in (Path(journal["backup"])/"egress").rglob("*"):
        if src.is_file(): copy_atomic(src,EGRESS/src.relative_to(Path(journal["backup"])/"egress"))
    start_services(); wait_health(); quick_check()
    if identities()!=journal["identities"]: raise UpdateError("identity_changed")


def apply(r,private,manifest):
    check_layout()
    if shutil.disk_usage(PRIVATE).free<1024*1024*1024: raise UpdateError("disk_full")
    package=private/"package"; extract_package(private/"aimili-vps-package.tar.gz",package)
    pairs=install_plan(package)+[(private/"x-ui-custom-linux-amd64",XUI_BINARY)]
    for src,_ in pairs: os.chmod(src,0o755 if src.name in ("aimili-gateway","aimili-gateway-admin","aimili-gateway-account","x-ui-custom-linux-amd64") else 0o644)
    info=json.loads(command([str(package/"bin/aimili-gateway"),"version","--json"]))
    if info.get("version")!=r["version"] or info.get("commit")!=manifest["gatewayCommit"]: raise UpdateError("version_invalid")
    snapshot=private/"backup"; snapshot.mkdir(mode=0o700)
    journal=dict(request=r,backup=str(snapshot),files=[],identities=identities(),phase="stopping")
    journal_path=PRIVATE/"active.json"
    atomic_json(journal_path,journal,0o600)
    progress(r,"validating",55,"backup")
    try:
        stop_services()
        quick_check()
        targets=[target for _,target in pairs]+[CONFIG,GATEWAY_DB,XUI_DB,PUBLIC/"current.json"]
        for index,target in enumerate(targets):
            saved=snapshot/str(index)
            if target.exists():
                if target in (GATEWAY_DB,XUI_DB):
                    with database(target) as source, database(saved) as dest: source.backup(dest)
                    shutil.copystat(target,saved); os.chown(saved,target.stat().st_uid,target.stat().st_gid)
                else: shutil.copy2(target,saved); os.chown(saved,target.stat().st_uid,target.stat().st_gid)
            journal["files"].append(dict(target=str(target),backup=str(saved) if saved.exists() else ""))
        shutil.copytree(EGRESS,snapshot/"egress",ignore=shutil.ignore_patterns("*.log","logs"))
        journal["phase"]="installing"; atomic_json(journal_path,journal,0o600)
        progress(r,"switching",65,"install")
        for src,target in pairs: copy_atomic(src,target)
        cfg=read_json(CONFIG); cfg["externalUiRoot"]=""
        for key in ("freesubFeedPath","freesubSingBoxPath","freesubStateDir","freesubSocksPort","freesubVLESSPort","freesubMixedPort"):
            cfg.pop(key,None)
        cfg_temp=private/"config.json"; atomic_json(cfg_temp,cfg)
        shutil.copystat(CONFIG,cfg_temp); os.chown(cfg_temp,CONFIG.stat().st_uid,CONFIG.stat().st_gid)
        copy_atomic(cfg_temp,CONFIG)
        progress(r,"verifying",80,"restart")
        start_services(); wait_health(); quick_check()
        progress(r,"verifying",95,"identities")
        if identities()!=journal["identities"]: raise UpdateError("identity_changed")
        atomic_json(PUBLIC/"current.json",dict(version=r["version"]))
        finish(r,"success")
        journal_path.unlink()
    except Exception:
        if journal["phase"]=="installing": rollback(journal)
        else: start_services(); wait_health()
        finish(r,"rolled_back","health_failed")
        journal_path.unlink()


def install(r):
    stage=STAGING/r["runId"]
    if not (stage/"ready.json").exists():
        try:
            p=read_json(stage/"progress.json")
            progress(r,"downloading",min(45,max(0,int(p.get("percent",0)))),"discover" if r["action"]=="check" else "download")
        except FileNotFoundError: pass
        return
    private=PRIVATE/"transactions"/r["runId"]
    private.mkdir(parents=True,mode=0o700,exist_ok=True)
    ready=read_json(stage/"ready.json")
    if ready.get("error"):
        allowed={"invalid_signature","unsupported_release","invalid_payload","release_missing","download_failed"}
        raise UpdateError(ready["error"] if ready["error"] in allowed else "download_failed")
    tag=ready.get("version","")
    if r["action"]=="apply" and tag!=r["version"]: raise UpdateError("invalid_payload")
    for name in ("manifest.json","manifest.sig"):
        (private/name).write_bytes(read_file(stage/name))
    manifest=verify_manifest(private,tag)
    current=read_json(PUBLIC/"current.json")["version"]
    if r["action"]=="check":
        check_layout()
        available=[dict(kind="project",version=tag,compatible=True)] if is_newer(tag,current) else []
        atomic_json(PUBLIC/"catalog.json",dict(capability=True,expiresAt=(dt.datetime.now(dt.timezone.utc)+dt.timedelta(hours=1)).isoformat(),available=available))
        progress(r,"verifying",95,"catalog"); finish(r,"success"); return
    if not is_newer(tag,current): raise UpdateError("version_invalid")
    progress(r,"validating",50,"signature")
    for key in ("package","xui"):
        asset=manifest["assets"][key]
        name=asset["name"]
        # Copy into a root-only directory before hashing; never execute downloader-owned files.
        copy_untrusted(stage/name,private/name,MAX_PACKAGE)
        if sha(private/name)!=asset["sha256"]: raise UpdateError("invalid_payload")
    apply(r,private,manifest)


def main(mode):
    import fcntl
    if mode not in ("fetch","install"): raise SystemExit(2)
    if mode=="install" and os.geteuid()!=0: raise SystemExit(2)
    lock=STAGING/".fetch.lock" if mode=="fetch" else PRIVATE/".install.lock"
    with open(lock,"a") as stream:
        try: fcntl.flock(stream,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError: return
        if mode=="install" and (PRIVATE/"active.json").exists():
            journal=read_json(PRIVATE/"active.json"); r=journal["request"]
            result_path=PUBLIC/"results"/(r["runId"]+".json")
            if result_path.exists():
                if read_json(result_path).get("state")=="repair_required": return
                (PRIVATE/"active.json").unlink()
                return
            try:
                progress(r,"switching",65,"rollback")
                if journal["phase"]=="installing": rollback(journal)
                else: start_services(); wait_health()
                finish(r,"rolled_back","interrupted"); (PRIVATE/"active.json").unlink()
            except Exception: finish(r,"repair_required","rollback_failed")
            return
        for path in sorted((PUBLIC/"requests").glob("*.json")):
            try: r=validate_request(read_json(path))
            except Exception: continue
            if path.stem!=r["runId"] or (PUBLIC/"results"/path.name).exists(): continue
            try: (fetch if mode=="fetch" else install)(r)
            except Exception as error:
                if mode=="install":
                    code=str(error) if isinstance(error,UpdateError) else "operation_failed"
                    finish(r,"repair_required" if (PRIVATE/"active.json").exists() else "failed",code)
            break


if __name__=="__main__": main(sys.argv[1])
