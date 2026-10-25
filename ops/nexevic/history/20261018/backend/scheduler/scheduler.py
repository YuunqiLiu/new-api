#!/usr/bin/env python3
"""Recover only system-disabled New API channels through server-side CAS probes."""

import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone
try:
    import fcntl
except ImportError:  # Unit tests can import the module on Windows.
    fcntl = None

API_BASE = os.environ.get("API_BASE", "http://127.0.0.1:3000").rstrip("/")
API_TOKEN = os.environ.get("API_TOKEN", "")
STATE_PATH = os.environ.get("STATE_PATH", "/var/lib/new-api-channel-scheduler/state.json")
LOCK_PATH = os.environ.get("LOCK_PATH", "/var/lib/new-api-channel-scheduler/scheduler.lock")
PEAK_CHANNEL_IDS = frozenset(item.strip() for item in os.environ.get("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "6,7,14,18").split(",") if item.strip())
LEGACY_ADOPT_IDS = frozenset(item.strip() for item in os.environ.get("NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS", "").split(",") if item.strip())
PROBE_INTERVAL = 15 * 60
SHANGHAI = timezone(timedelta(hours=8), "Asia/Shanghai")
ENABLED, MANUALLY_DISABLED, AUTO_DISABLED = 1, 2, 3


def log(message):
    print(time.strftime("%Y-%m-%d %H:%M:%S"), message, flush=True)


def api(method, path, payload=None, timeout=120):
    if not API_TOKEN:
        raise RuntimeError("API_TOKEN is required")
    body = None if payload is None else json.dumps(payload).encode("utf-8")
    headers = {"Authorization": "Bearer " + API_TOKEN}
    if body is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(API_BASE + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            result = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raise RuntimeError("New API HTTP {}".format(exc.code)) from None
    if not result.get("success"):
        raise RuntimeError("New API operation was rejected")
    return result


def load_state():
    try:
        with open(STATE_PATH, "r", encoding="utf-8") as handle:
            state = json.load(handle)
    except FileNotFoundError:
        state = {"version": 2, "managed": {}}
    if state.get("version", 1) < 2:
        state["legacy_managed"] = state.get("managed", {})
        state["managed"] = {}
        state["version"] = 2
    state.setdefault("managed", {})
    state.setdefault("legacy_managed", {})
    return state


def save_state(state):
    temporary = STATE_PATH + ".tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        json.dump(state, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(temporary, 0o600)
    os.replace(temporary, STATE_PATH)


def list_channels():
    result = api("GET", "/api/channel/?p=0&page_size=1000")
    return result["data"]["items"]


def is_peak_blocked(channel_id, now=None):
    if str(channel_id) not in PEAK_CHANNEL_IDS:
        return False
    current = (now or datetime.now(timezone.utc)).astimezone(SHANGHAI)
    minute = current.hour * 60 + current.minute
    return 13 * 60 + 30 <= minute < 18 * 60


def channel_status_info(channel):
    value = channel.get("other_info") or {}
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except (ValueError, TypeError):
            return {}
    return value if isinstance(value, dict) else {}


def quota_reset_at(reason):
    match = re.search(r"\breset_at=([0-9]{10})\b", str(reason))
    return int(match.group(1)) if match else 0


def adopt_legacy_records(channels, state):
    for channel_id, record in list(state["legacy_managed"].items()):
        channel = channels.get(channel_id)
        if channel_id not in LEGACY_ADOPT_IDS or not channel:
            continue
        info = channel_status_info(channel)
        if (channel.get("status") != MANUALLY_DISABLED
                or info.get("status_reason") != "manual operation"
                or info.get("status_time") != record.get("disabled_at")):
            log("legacy channel #{} ownership changed; left untouched".format(channel_id))
            continue
        try:
            result = api("POST", "/api/channel/{}/recovery/adopt".format(channel_id), {
                "expected_version": channel.get("status_version", 0),
                "legacy_disabled_at": record.get("disabled_at", 0),
                "legacy_status_reason": info["status_reason"],
                "legacy_quota_reason": record.get("reason", ""),
            })
        except Exception as exc:
            log("legacy channel #{} adoption failed ({}); left untouched".format(channel_id, type(exc).__name__))
            continue
        if result["data"].get("adopted"):
            del state["legacy_managed"][channel_id]
            log("adopted verified legacy scheduler record for channel #{}".format(channel_id))


def reconcile_recovery(channels, state, now=None):
    timestamp = int(time.time() if now is None else now)
    managed = state["managed"]
    for channel_id in list(managed):
        if channel_id not in channels or channels[channel_id].get("status") != AUTO_DISABLED:
            del managed[channel_id]
    for channel_id, channel in channels.items():
        if channel.get("status") != AUTO_DISABLED:
            continue  # No enabled-channel polling and no manual-stop recovery.
        info = channel_status_info(channel)
        reason = str(info.get("status_reason", ""))
        version = int(channel.get("status_version", 0))
        entry = managed.get(channel_id)
        if not entry or entry.get("status_version") != version:
            disabled_at = int(info.get("status_time") or timestamp)
            entry = {"status_version": version, "next_probe_at": max(disabled_at + PROBE_INTERVAL, quota_reset_at(reason))}
            managed[channel_id] = entry
        if entry.get("suspended") or "[auth]" in reason or "[model]" in reason:
            continue
        if timestamp < max(entry.get("next_probe_at", 0), quota_reset_at(reason)):
            continue
        if is_peak_blocked(channel_id, datetime.fromtimestamp(timestamp, timezone.utc)):
            continue
        try:
            result = api("POST", "/api/channel/{}/recovery".format(channel_id), {"expected_version": version})
        except Exception as exc:
            entry["next_probe_at"] = timestamp + PROBE_INTERVAL
            log("channel #{} recovery check failed ({})".format(channel_id, type(exc).__name__))
            continue
        data = result["data"]
        outcome = data.get("outcome")
        if outcome == "recovered":
            managed.pop(channel_id, None)
            log("channel #{} recovered after server-side generation check".format(channel_id))
        elif outcome == "stale":
            managed.pop(channel_id, None)
            log("channel #{} changed during recovery; left untouched".format(channel_id))
        else:
            entry["status_version"] = int(data.get("status_version", version))
            entry["next_probe_at"] = max(timestamp + PROBE_INTERVAL, int(data.get("next_probe_at", 0)))
            entry["suspended"] = data.get("classification") in ("auth", "model")
            log("channel #{} recovery outcome: {}".format(channel_id, outcome))


def list_users():
    result = api("GET", "/api/user/?p=0&page_size=1000")
    return result["data"]["items"]


def reconcile_oidc_usernames():
    users = list_users()
    occupied = {
        str(user.get("username", "")).lower()
        for user in users
        if not user.get("oidc_id")
    }
    for user in sorted(users, key=lambda item: int(item["id"])):
        username = str(user.get("username", ""))
        email = str(user.get("email", ""))
        if not user.get("oidc_id") or "@" not in email:
            continue

        base = email.split("@", 1)[0].strip().lower()
        base = re.sub(r"[^a-z0-9._-]+", "-", base).strip("-._")
        if not base:
            base = "user{}".format(user["id"])
        base = base[:20]
        candidate = base
        suffix = 2
        while candidate.lower() in occupied:
            marker = "-{}".format(suffix)
            candidate = base[: 20 - len(marker)] + marker
            suffix += 1

        if username == candidate:
            occupied.add(candidate.lower())
            continue

        api("PUT", "/api/user/", {
            "id": int(user["id"]),
            "username": candidate,
            "display_name": user.get("display_name") or candidate,
            "group": user.get("group") or "default",
        })
        occupied.add(candidate.lower())
        log("renamed OIDC user #{} from {} to {}".format(
            user["id"], username, candidate
        ))


def main():
    if fcntl is None:
        raise RuntimeError("The scheduler requires a Linux file lock")
    os.makedirs(os.path.dirname(STATE_PATH), mode=0o700, exist_ok=True)
    with open(LOCK_PATH, "w") as lock_handle:
        try:
            fcntl.flock(lock_handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            log("another scheduler run is active; exiting")
            return 0
        state = load_state()
        try:
            reconcile_oidc_usernames()
        except Exception as exc:
            log("OIDC username reconciliation failed ({}); recovery continues".format(type(exc).__name__))
        channels = {str(item["id"]): item for item in list_channels()}
        adopt_legacy_records(channels, state)
        if LEGACY_ADOPT_IDS:
            channels = {str(item["id"]): item for item in list_channels()}
        reconcile_recovery(channels, state)
        save_state(state)
        log("run complete: {} system-disabled channel(s) tracked".format(len(state["managed"])))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        log("fatal ({})".format(type(exc).__name__))
        sys.exit(1)
