"""持久化每个出口的一次性自动修复状态。"""

from __future__ import annotations

import json
import threading
import time
from pathlib import Path
from typing import Any, Callable
import recovery_policy


class RepairStore:
    """确保同一出口的同一次故障最多领取一次自动修复机会。"""

    def __init__(self, path: Path, now: Callable[[], float] = time.time):
        self.path = path
        self.now = now
        self.lock = threading.RLock()

    def _read(self) -> dict[str, Any]:
        try:
            document = json.loads(self.path.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, json.JSONDecodeError):
            return {"version": 1, "egresses": {}}
        if not isinstance(document, dict) or not isinstance(document.get("egresses"), dict):
            return {"version": 1, "egresses": {}}
        return document

    def _write(self, document: dict[str, Any]) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        temporary = self.path.with_suffix(self.path.suffix + ".tmp")
        temporary.write_text(
            json.dumps(document, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        temporary.replace(self.path)

    def get(self, egress: str) -> dict[str, Any]:
        with self.lock:
            row = self._read()["egresses"].get(str(egress), {})
            return dict(row) if isinstance(row, dict) else {}

    def claim(self, egress: str, candidate_id: str, country: str) -> bool:
        """领取本次故障唯一的自动修复机会；已经领取或待人工时返回 False。"""
        key = str(egress)
        candidate_id = str(candidate_id or "").strip()
        country = str(country or "").strip().upper()
        with self.lock:
            document = self._read()
            current = document["egresses"].get(key, {})
            unresolved = (
                isinstance(current, dict)
                and current.get("status") in {"repairing", "manual_required"}
            )
            if unresolved:
                return False
            document["egresses"][key] = {
                "status": "repairing",
                "failed_candidate_id": candidate_id,
                "country": country,
                "attempt_count": 1,
                "attempted_at": self.now(),
                "replacement_candidate_id": "",
                "error_code": "",
            }
            self._write(document)
            return True

    def require_manual(
        self,
        egress: str,
        error_code: str,
        replacement_candidate_id: str = "",
    ) -> None:
        with self.lock:
            document = self._read()
            current = document["egresses"].setdefault(str(egress), {})
            current.update({
                "status": "manual_required",
                "attempt_count": max(1, int(current.get("attempt_count") or 0)),
                "replacement_candidate_id": str(replacement_candidate_id or ""),
                "error_code": str(error_code or "auto_repair_failed"),
                "finished_at": self.now(),
            })
            self._write(document)

    def wait_for_standby(self, egress: str, candidate_id: str, country: str) -> None:
        """幂等记录等待热备；周期检查不能重置故障时间预算。"""
        with self.lock:
            document = self._read()
            row = document['egresses'].get(egress, {})
            if row.get('status') in ('waiting_standby', 'manual_required'):
                return
            document['egresses'][egress] = dict(
                status='waiting_standby', failed_candidate_id=candidate_id,
                country=country, started_at=self.now(), attempt_count=0,
                error_code='recovery_pending', recovery_version=2,
            )
            self._write(document)

    def begin_round(self, egress: str, country: str, settings: dict) -> bool:
        with self.lock:
            document = self._read()
            row = document['egresses'].get(egress, {})
            now = self.now()
            if row.get('status') in ('manual_required', 'repairing'):
                return False
            if row.get('status') == 'healthy':
                row = {}
            started = row.get('started_at', now)
            if now - started >= settings['recoveryBudgetSeconds']:
                row.update(status='manual_required', error_code='recovery_budget_exhausted', next_attempt_at=0)
                document['egresses'][egress] = row
                self._write(document)
                return False
            if row.get('next_attempt_at', 0) > now:
                return False
            row.update(status='repairing', country=country, started_at=started,
                       attempted_at=now, recovery_version=2)
            document['egresses'][egress] = row
            self._write(document)
            return True

    def finish_round(self, egress: str, settings: dict, error_code: str) -> dict:
        with self.lock:
            document = self._read()
            row = document['egresses'].setdefault(egress, {})
            result = recovery_policy.failed_round(dict(
                startedAt=row.get('started_at', self.now()),
                attempt=row.get('attempt_count', 0),
            ), self.now(), settings, error_code)
            row.update(status=result['status'], started_at=result['startedAt'],
                       attempt_count=result['attempt'], next_attempt_at=result['nextAttemptAt'],
                       error_code=result['lastErrorCode'], recovery_version=2)
            self._write(document)
            return dict(row)

    def cool_candidate(self, egress: str, candidate_id: str, seconds: int) -> None:
        with self.lock:
            document = self._read()
            row = document['egresses'].setdefault(egress, {})
            now = self.now()
            cooldowns = {key: until for key, until in row.get('cooldowns', {}).items() if until > now}
            cooldowns[candidate_id] = now + seconds
            row['cooldowns'] = cooldowns
            self._write(document)

    def recover_interrupted(self) -> list[str]:
        """把上次进程中断时未结束的自动修复转为人工处理。"""
        recovered: list[str] = []
        with self.lock:
            document = self._read()
            for key, current in document["egresses"].items():
                if not isinstance(current, dict) or current.get("status") != "repairing":
                    continue
                if current.get('recovery_version') == 2:
                    current.update(status='retry_wait', next_attempt_at=self.now(), error_code='recovery_resumed')
                    recovered.append(str(key))
                    continue
                current.update({
                    "status": "manual_required",
                    "attempt_count": max(1, int(current.get("attempt_count") or 0)),
                    "error_code": "repair_interrupted",
                    "finished_at": self.now(),
                })
                recovered.append(str(key))
            if recovered:
                self._write(document)
        return recovered

    def mark_healthy(self, egress: str, candidate_id: str) -> None:
        with self.lock:
            document = self._read()
            current = document["egresses"].get(str(egress), {})
            if (
                isinstance(current, dict)
                and current.get("status") == "healthy"
                and str(current.get("candidate_id") or "") == str(candidate_id or "")
            ):
                return
            document["egresses"][str(egress)] = {
                "status": "healthy",
                "candidate_id": str(candidate_id or ""),
                "attempt_count": 0,
                "error_code": "",
                "healthy_at": self.now(),
            }
            self._write(document)

    def clear(self, egress: str) -> None:
        with self.lock:
            document = self._read()
            if document["egresses"].pop(str(egress), None) is not None:
                self._write(document)
