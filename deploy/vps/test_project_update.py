import io
import json
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import os
import sqlite3
import shutil
import subprocess
from pathlib import Path
import project_update as u
import project_update_boot as boot


class ProjectUpdateTests(unittest.TestCase):
    def test_bridge_and_project_tags_keep_old_worker_on_safe_channel(self):
        releases=[dict(tag_name="v0.2.14-vps"),dict(tag_name="project-v0.2.15-vps"),
                  dict(tag_name="project-v0.2.16-vps",draft=True)]
        self.assertEqual(u.release_versions(releases),{"v0.2.14-vps":"v0.2.14-vps",
                                                       "v0.2.15-vps":"project-v0.2.15-vps"})
        self.assertEqual(max(u.release_versions(releases),key=u.version_tuple),"v0.2.15-vps")
        self.assertFalse(u.VERSION.fullmatch(releases[1]["tag_name"]))

    def test_engine_handoff_and_boot_fallback_keep_old_slot(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder); slots=root/"slots";slots.mkdir()
            old=slots/("engine-2-"+("a"*12)+".py")
            old.write_text("old engine")
            previous=dict(file=old.name,sha256=u.sha(old))
            (slots/"current.json").write_text(json.dumps(dict(current=previous,previous=None)))
            update=root/"project-update-engine.py"; update.write_text("new engine")
            digest=u.sha(update)
            manifest=dict(updater=dict(version=3),assets=dict(updater=dict(name=update.name,sha256=digest)))
            with patch.object(u,"SLOTS",slots),patch.object(u,"LIB",root),patch.object(u,"updater_pointer",side_effect=lambda: json.loads((slots/"current.json").read_text())),patch.object(boot,"SLOTS",slots),patch.object(boot,"POINTER",slots/"current.json"):
                u.promote_engine(root,manifest)
                state=json.loads((slots/"current.json").read_text())
                self.assertEqual(state["previous"],previous)
                self.assertEqual(state["current"]["sha256"],digest)
                with patch.object(boot,"safe_file",side_effect=lambda path,*args: Path(path).read_bytes()),patch.object(boot.subprocess,"run",return_value=type("Result",(),{"returncode":1})()),patch.object(boot,"record_failure") as failure:
                    self.assertEqual(boot.main("install"),1)
                    failure.assert_called_once()
                state=json.loads((slots/"current.json").read_text())
                self.assertEqual(state["current"],previous)
                self.assertEqual(state["failedHash"],digest)
                with self.assertRaisesRegex(u.UpdateError,"updater_engine_failed"):
                    u.promote_engine(root,manifest)

    def test_bridge_adopts_signed_worker_as_fixed_bootloader(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);public=root/"public";private=root/"private";lib=root/"lib"
            for path in (public/"results",private/"transactions",lib): path.mkdir(parents=True)
            version="v0.2.14-vps";run="b"*64;txn=private/"transactions"/run
            package=txn/"package/deploy/vps";package.mkdir(parents=True)
            (txn/"backup").mkdir()
            source=Path(u.__file__);worker=lib/"project_update.py";shutil.copyfile(source,worker)
            shutil.copyfile(source,package/"project_update.py")
            (package/"project_update_boot.py").write_text("bootloader")
            (txn/"backup/3").write_text("def install(r):\n    return None\n")
            (txn/"aimili-vps-package.tar.gz").write_text("archive")
            (public/"current.json").write_text(json.dumps(dict(version=version)))
            (public/"results"/(run+".json")).write_text(json.dumps(dict(state="success",version=version)))
            manifest=dict(assets=dict(package=dict(name="aimili-vps-package.tar.gz",sha256=u.sha(txn/"aimili-vps-package.tar.gz"))))
            with patch.object(u,"PUBLIC",public),patch.object(u,"PRIVATE",private),patch.object(u,"LIB",lib),patch.object(u,"SLOTS",lib/"update-engines"),patch.object(u,"BOOT",worker),patch.object(u,"verify_manifest",return_value=manifest),patch.object(u,"signed_bootstrap_matches",return_value=True),patch.object(u,"install_plan",return_value=[1,2,3]):
                u.adopt_bootloader()
                state=json.loads((lib/"update-engines/current.json").read_text())
                self.assertEqual(state["current"]["sha256"],u.sha(source))
                self.assertIsNotNone(state["previous"])
                self.assertEqual(worker.read_text(),"bootloader")

    def test_preflight_reports_specific_blocker_and_upgrade_path(self):
        manifest = dict(
            schemaVersion=1, updateContract=2, release="v0.2.14-vps",
            compatibility=dict(platform="linux-amd64", osRelease="ubuntu-24.04", layout="unified-v1", gatewaySchemaMin=1,
                               gatewaySchemaMax=14, requiredFreeBytes=1024),
            components=[dict(name=name,action=action) for name,action in u.REQUIRED_ACTIONS.items()],
            updater=dict(minVersion=2, version=2),
        )
        facts = dict(platform="linux-amd64",osRelease="ubuntu-24.04",layout="unified-v1",gatewaySchema=13,freeBytes=2048,updaterVersion=2)
        self.assertEqual(u.assess_compatibility(manifest,facts)["compatible"],True)
        cases = [
            (dict(platform="linux-arm64"),"platform_unsupported"),
            (dict(osRelease="ubuntu-22.04"),"platform_unsupported"),
            (dict(layout="legacy-vpngate"),"unsupported_layout"),
            (dict(gatewaySchema=15),"database_too_new"),
            (dict(freeBytes=900),"disk_full"),
            (dict(updaterVersion=1),"updater_upgrade_required"),
        ]
        for override, reason in cases:
            with self.subTest(reason=reason):
                report=u.assess_compatibility(manifest,dict(facts,**override))
                self.assertFalse(report["compatible"])
                self.assertEqual(report["reasonCode"],reason)
                self.assertTrue(report["upgradePath"].startswith("https://github.com/thzyh/aimili-gateway/blob/main/docs/upgrade.md#"))
        next(item for item in manifest["components"] if item["name"]=="caddy")["action"]="replace"
        report=u.assess_compatibility(manifest,facts)
        self.assertEqual(report["reasonCode"],"unsupported_component")
        self.assertEqual(report["component"],"caddy")
        manifest["updateContract"]=3
        manifest["updater"]=dict(minVersion=3,maxVersion=3,version=3)
        manifest["assets"]=dict(updater=dict(name="project-update-engine.py",sha256="a"*64))
        self.assertEqual(u.assess_compatibility(manifest,facts)["requiresUpdaterUpgrade"],True)

    def test_real_signature_rejects_tampered_manifest(self):
        openssl=shutil.which("openssl") or "D:/SoftWare/Git/mingw64/bin/openssl.exe"
        if not Path(openssl).exists(): self.skipTest("openssl unavailable")
        if subprocess.run([openssl,"version"],capture_output=True,text=True).stdout.startswith("OpenSSL 1.1"):
            self.skipTest("Ed25519 -rawin requires OpenSSL 3")
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder); private=root/"private.pem"; public=root/"public.pem"
            original=u.command
            def cmd(args,**kwargs): return original([openssl]+args[1:] if args[0]=="openssl" else args,**kwargs)
            with patch.object(u,"command",side_effect=cmd),patch.object(u,"KEY",public):
                cmd(["openssl","genpkey","-algorithm","ED25519","-out",str(private)])
                cmd(["openssl","pkey","-in",str(private),"-pubout","-out",str(public)])
                manifest=dict(schemaVersion=1,updateContract=1,release="v0.2.12-vps",gatewayCommit="b"*40,assets=dict(package=dict(name="aimili-vps-package.tar.gz",sha256="c"*64),xui=dict(name="x-ui-custom-linux-amd64",sha256="d"*64)))
                (root/"manifest.json").write_text(json.dumps(manifest))
                cmd(["openssl","pkeyutl","-sign","-rawin","-inkey",str(private),"-in",str(root/"manifest.json"),"-out",str(root/"manifest.sig")])
                self.assertEqual(u.verify_manifest(root,"v0.2.12-vps")["release"],"v0.2.12-vps")
                (root/"manifest.json").write_text(json.dumps(dict(manifest,gatewayCommit="a"*40)))
                with self.assertRaisesRegex(u.UpdateError,"invalid_signature"): u.verify_manifest(root,"v0.2.12-vps")

    def test_install_preserves_config_and_rolls_back_database_on_failure(self):
        for fail in (False,True):
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as folder:
                root=Path(folder)
                names={key:root/key for key in ("PUBLIC","PRIVATE","CONFIG","EGRESS","GATEWAY_DB","XUI_DB","XUI_BINARY")}
                for key in ("PUBLIC","PRIVATE","EGRESS"): names[key].mkdir()
                names["CONFIG"].write_text(json.dumps(dict(publicOrigin="https://example.test",externalUiRoot="old-ui",projectUpdateEnabled=True)))
                names["XUI_BINARY"].write_text("old-xui")
                (names["PUBLIC"]/"current.json").write_text(json.dumps(dict(version="legacy-test")))
                (names["EGRESS"]/"slots.json").write_text("preserved")
                for key in ("GATEWAY_DB","XUI_DB"):
                    with u.database(names[key]) as db: db.execute("create table marker (value text)"); db.execute("insert into marker values ('old')")
                txn=names["PRIVATE"]/"txn"; txn.mkdir()
                (txn/"x-ui-custom-linux-amd64").write_text("new-xui")
                target=root/"gateway"; target.write_text("old-gateway")
                source=root/"new-gateway"; source.write_text("new-gateway")
                request=dict(runId="a"*64,kind="project",action="apply",version="v0.2.12-vps")
                def start():
                    if target.read_text()=="new-gateway":
                        with u.database(names["GATEWAY_DB"]) as db: db.execute("update marker set value='new'")
                patches=[patch.object(u,k,v) for k,v in names.items()]
                for item in patches: item.start()
                try:
                    with patch.object(u,"check_layout"), patch.object(u,"extract_package"), patch.object(u,"install_plan",return_value=[(source,target)]), patch.object(u,"identities",return_value="same"), patch.object(u,"stop_services"), patch.object(u,"start_services",side_effect=start), patch.object(u,"wait_health",side_effect=[u.UpdateError("health_failed"),None] if fail else None), patch.object(u,"command",return_value=json.dumps(dict(version=request["version"],commit="b"*40)).encode()), patch.object(os,"chown",create=True), patch.object(u.shutil,"disk_usage",return_value=type("Space",(),{"free":2**32})()):
                        u.apply(request,txn,dict(gatewayCommit="b"*40))
                    result=json.loads((names["PUBLIC"]/"results"/(request["runId"]+".json")).read_text())
                    self.assertEqual(result["state"],"rolled_back" if fail else "success")
                    self.assertEqual(target.read_text(),"old-gateway" if fail else "new-gateway")
                    cfg=json.loads(names["CONFIG"].read_text())
                    self.assertEqual(cfg["publicOrigin"],"https://example.test")
                    self.assertEqual(cfg["externalUiRoot"],"old-ui" if fail else "")
                    with u.database(names["GATEWAY_DB"]) as db: self.assertEqual(db.execute("select value from marker").fetchone()[0],"old" if fail else "new")
                finally:
                    for item in reversed(patches): item.stop()

    def test_request_pins_version_and_rejects_untrusted_paths(self):
        request = dict(runId="a"*64, kind="project", action="check", version="")
        self.assertEqual(u.validate_request(request)["action"], "check")
        for version in ("latest", "../../etc/passwd", "v01.2.3-vps", "v1.2.3"):
            with self.assertRaises(u.UpdateError):
                u.validate_request(dict(request, action="apply", version=version))

    def test_archive_rejects_traversal_symlinks_and_duplicates(self):
        for name, kind in (("../bad", tarfile.REGTYPE), ("/bad", tarfile.REGTYPE), ("link", tarfile.SYMTYPE), ("duplicate", tarfile.REGTYPE)):
            with tempfile.TemporaryDirectory() as root:
                archive = Path(root)/"a.tar.gz"
                with tarfile.open(archive, "w:gz") as tf:
                    item = tarfile.TarInfo(name); item.type = kind
                    tf.addfile(item, io.BytesIO())
                    if name == "duplicate": tf.addfile(item, io.BytesIO())
                with self.assertRaises(u.UpdateError): u.extract_package(archive, Path(root)/"out")

    def test_version_order_and_no_downgrade(self):
        self.assertGreater(u.version_tuple("v0.2.12-vps"), u.version_tuple("v0.2.9-vps"))
        self.assertFalse(u.is_newer("v0.2.12-vps", "v0.2.12-vps"))
        self.assertFalse(u.is_newer("v0.2.11-vps", "v0.2.12-vps"))
        self.assertTrue(u.is_newer("v0.2.12-vps", "legacy-eea92cb"))

    def test_terminal_result_is_not_a_progress_record(self):
        with tempfile.TemporaryDirectory() as root:
            old = u.PUBLIC; u.PUBLIC = Path(root)
            try:
                r=dict(runId="a"*64,kind="project",action="apply",version="v0.2.12-vps")
                u.progress(r,"switching",65,"install")
                self.assertFalse((u.PUBLIC/"results"/(r["runId"]+".json")).exists())
                u.finish(r,"rolled_back","health_failed")
                result=json.loads((u.PUBLIC/"results"/(r["runId"]+".json")).read_text())
                self.assertEqual(result["state"],"rolled_back")
                self.assertNotIn("percent",result)
            finally: u.PUBLIC=old


if __name__ == "__main__": unittest.main()
