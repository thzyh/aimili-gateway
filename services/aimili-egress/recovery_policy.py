"""专属备用的纯策略：参数、国家统计、优先级和有界退避。"""
from __future__ import annotations
import math

DEFAULTS = {
    'failureThreshold': 3, 'healthIntervalSeconds': 30,
    'standbyIntervalSeconds': 30, 'standbyFailureThreshold': 2,
    'dialTimeoutSeconds': 25, 'candidatesPerRound': 8,
    'maxConcurrentDials': 1, 'retryInitialSeconds': 10,
    'retryMaxSeconds': 120, 'candidateCooldownSeconds': 600,
    'recoveryBudgetSeconds': 1800, 'freshnessSeconds': 120,
    'allowCrossCountry': True, 'allowDatacenter': True,
}
BOUNDS = {
    'failureThreshold': (1, 10), 'healthIntervalSeconds': (10, 300),
    'standbyIntervalSeconds': (10, 300), 'standbyFailureThreshold': (1, 10),
    'dialTimeoutSeconds': (10, 120), 'candidatesPerRound': (1, 32),
    'maxConcurrentDials': (1, 4), 'retryInitialSeconds': (5, 300),
    'retryMaxSeconds': (10, 1800), 'candidateCooldownSeconds': (30, 86400),
    'recoveryBudgetSeconds': (60, 86400), 'freshnessSeconds': (30, 3600),
}


def validate(values: dict) -> dict:
    if not isinstance(values, dict) or set(values) != set(DEFAULTS):
        raise ValueError('invalid_recovery_settings')
    for key, default in DEFAULTS.items():
        value = values[key]
        if type(value) is not type(default):
            raise ValueError('invalid_recovery_settings')
        if key in BOUNDS and not BOUNDS[key][0] <= value <= BOUNDS[key][1]:
            raise ValueError('invalid_recovery_settings')
    if values['retryMaxSeconds'] < values['retryInitialSeconds']:
        raise ValueError('invalid_recovery_settings')
    if values['recoveryBudgetSeconds'] < values['dialTimeoutSeconds']:
        raise ValueError('invalid_recovery_settings')
    return dict(values)


def targets(slots: list[int]) -> list[dict]:
    # 稳定编号：删除中间出口不会把后面的备用移绑到别的目标。
    return [dict(index=0, target='main', countries=[])] + [
        dict(index=i + 1, target=f'slot:{i}', countries=[]) for i in sorted(set(slots))
    ]


def residential(node: dict) -> bool:
    return str(node.get('ip_type') or '').lower() in ('residential', 'mobile')


def number(value, default=0):
    try:
        result = float(value)
        return result if math.isfinite(result) and result >= 0 else default
    except (TypeError, ValueError):
        return default


def country_statistics(nodes: list[dict], now: float, freshness: int) -> list[dict]:
    countries = {}
    seen = set()
    for node in sorted(nodes, key=lambda n: -number(n.get('probed_at'))):
        code = str(node.get('country_short') or '').upper()
        identity = str(node.get('exit_ip') or node.get('ip') or node.get('id') or '')
        if len(code) != 2 or not code.isalpha() or not identity or identity in seen:
            continue
        row = countries.setdefault(code, dict(
            code=code, dialableCount=0, freshEgressCount=0,
            residentialCount=0, datacenterCount=0, checkedAt=0,
            ipQualityPassCount=None, ipQualityStatus='not_implemented',
        ))
        if node.get('probe_status') != 'available' or not node.get('config_text'):
            continue
        seen.add(identity)
        row['dialableCount'] += 1
        row['residentialCount' if residential(node) else 'datacenterCount'] += 1
        checked = number(node.get('probed_at'))
        row['checkedAt'] = max(row['checkedAt'], checked)
        if node.get('exit_ip') and 0 <= now - checked <= freshness:
            row['freshEgressCount'] += 1
    return sorted(countries.values(), key=lambda r: (
        -r['freshEgressCount'], -r['dialableCount'], -r['residentialCount'], r['code']))


def rank_candidates(nodes: list[dict], country: str, settings: dict, now: float) -> list[dict]:
    eligible = [n for n in nodes if n.get('probe_status') == 'available' and n.get('config_text')
                and (settings['allowDatacenter'] or residential(n))
                and (settings['allowCrossCountry'] or n.get('country_short') == country)]
    stats = country_statistics(eligible, now, settings['freshnessSeconds'])
    country_rank = {row['code']: i + 1 for i, row in enumerate(stats)}
    country_rank[country] = 0
    return sorted(eligible, key=lambda n: (
        country_rank.get(n.get('country_short'), len(stats) + 1),
        0 if residential(n) else 1,
        number(n.get('egress_latency_ms') or n.get('latency_ms'), 999999),
        str(n.get('id') or ''),
    ))


def failed_round(state: dict, now: float, settings: dict, reason: str) -> dict:
    started = number(state.get('startedAt'), now)
    if started <= 0 or started > now:
        started = now
    attempt = int(number(state.get('attempt'), 0)) + 1
    delay = min(settings['retryMaxSeconds'], settings['retryInitialSeconds'] * 2 ** min(attempt - 1, 16))
    manual = now - started >= settings['recoveryBudgetSeconds']
    return dict(startedAt=started, attempt=attempt,
                status='manual_required' if manual else 'retry_wait',
                nextAttemptAt=0 if manual else now + delay, lastErrorCode=reason)
