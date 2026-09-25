#!/usr/bin/env python3
"""固定更新入口：只运行 root 所有、摘要匹配的更新引擎，并在坏引擎时恢复旧槽。"""
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

LIB = Path("/usr/local/lib/aimili-gateway")
SLOTS = LIB / "update-engines"
POINTER = SLOTS / "current.json"
PUBLIC = Path("/var/lib/aimili-gateway/project-update")
PRIVATE = Path("/var/lib/aimili-gateway-project-update")
SHA = re.compile(r"[0-9a-f]{64}\Z")


def safe_file(path, limit=512*1024, trusted_root=True):
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, "rb") as stream:
        info=os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_size>limit or info.st_mode&0o022:
            raise RuntimeError("untrusted_engine_state")
        if trusted_root and os.name!="nt" and info.st_uid!=0: raise RuntimeError("untrusted_engine_owner")
        return stream.read(limit+1)


def sha(path):
    return hashlib.sha256(safe_file(path)).hexdigest()


def read_pointer():
    value=json.loads(safe_file(POINTER,16384))
    for key in ("current","previous"):
        item=value.get(key)
        if item is not None:
            if not isinstance(item,dict) or not isinstance(item.get("file"),str) or not isinstance(item.get("sha256"),str) or not re.fullmatch(r"engine-[0-9]+-[a-f0-9]{12}\.py",item["file"]) or not SHA.fullmatch(item["sha256"]):
                raise RuntimeError("invalid_engine_pointer")
    if not value.get("current"): raise RuntimeError("engine_missing")
    return value


def write_pointer(value):
    temp=POINTER.with_suffix(".tmp")
    with open(temp,"w",encoding="utf-8") as stream:
        os.chmod(temp,0o644)
        json.dump(value,stream,separators=(",",":"))
        stream.flush();os.fsync(stream.fileno())
    os.replace(temp,POINTER)
    if os.name!="nt":
        fd=os.open(SLOTS,os.O_DIRECTORY);os.fsync(fd);os.close(fd)


def record_failure(code,state):
    """Never retry an uncertain transaction with another engine."""
    lease=PUBLIC/"requests/.update.lease"
    try: run=json.loads(safe_file(lease,4096,False))["runId"]
    except Exception: return
    if not re.fullmatch(r"[a-f0-9]{64}",run): return
    result=PUBLIC/"results"/(run+".json")
    if result.exists(): return
    if (PRIVATE/"active.json").exists(): code="rollback_failed"; terminal="repair_required"
    else: terminal="failed"
    value=dict(runId=run,kind="project",state=terminal,errorCode=code,
               upgradePath="https://github.com/thzyh/aimili-gateway/blob/main/docs/upgrade.md#"+code.replace("_","-"))
    import grp
    temp=result.with_suffix(".tmp")
    with open(temp,"w",encoding="utf-8") as stream:
        os.chmod(temp,0o640);json.dump(value,stream);stream.flush();os.fsync(stream.fileno())
    os.chown(temp,0,grp.getgrnam("aimili-gateway").gr_gid)
    os.replace(temp,result)


def main(mode):
    if mode not in ("fetch","install"): raise SystemExit(2)
    try: state=read_pointer()
    except Exception:
        if mode=="install": record_failure("updater_engine_failed",{})
        return 1
    current=state["current"]
    try:
        if sha(SLOTS/current["file"])!=current["sha256"]: raise RuntimeError("engine_hash_mismatch")
        rc=subprocess.run([sys.executable,str(SLOTS/current["file"]),mode]).returncode
    except Exception: rc=1
    if rc==0: return 0
    if mode=="install":
        if state.get("previous"):
            try:
                previous=state["previous"]
                if previous["sha256"]==state.get("failedHash"): raise RuntimeError("previous_engine_already_failed")
                if sha(SLOTS/previous["file"])!=previous["sha256"]: raise RuntimeError("previous_engine_hash_mismatch")
                state["failedHash"]=current["sha256"]
                state["current"],state["previous"]=previous,current
                write_pointer(state)
            except Exception:
                record_failure("updater_engine_failed",state)
                raise
        record_failure("updater_engine_failed",state)
    return rc or 1


if __name__=="__main__": raise SystemExit(main(sys.argv[1]))
