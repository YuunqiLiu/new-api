#!/usr/bin/env python3
"""Apply the reviewed NexEvic routing configuration (Python 3.6, stdlib only).

Default: GET-only dry run.  --apply is the only way to issue mutations.
The caller must first start the reviewed, gated candidate on 127.0.0.1:3019.
No tests, quota refreshes, key-export APIs, or requests to upstream providers
are issued.  The env file's API_BASE is intentionally ignored.

Verified contracts: router/channel-router.go, controller/channel.go,
controller/channel_authz.go, router/api-router.go, controller/option.go and
setting/operation_setting/monitor_setting.go from the actual deployment.
The partial-update HTTP method is PUT, not PATCH.  Status has a separate POST.
"""
from __future__ import print_function

import argparse
import copy
import datetime
import json
import os
import pathlib
import sys
import tempfile
import urllib.error
import urllib.request


BASE_URL = "http://127.0.0.1:3019"
ENV_FILE = "/home/deploy/new_api_channel_scheduler/scheduler.env"
KEY_FILE = "/home/deploy/deploy_new_api/backups/routing-20261018/private/baidu-2.key"
BACKUP_DIR = "/home/deploy/deploy_new_api/backups/routing-20261018/config"
PLAN_ID = "nexevic-routing-20261018-v1"
BAIDU_NAME = "百度千帆 #2"
BAIDU_MARKER = "nexevic-routing-20261018:baidu-personal-max-2"
GLM_MODELS = ("glm-5.3", "glm-5.3-flash")
GLM_IDS = (6, 7, 14, 18)
EXISTING_IDS = (3, 4, 6, 7, 14, 15, 18)
EXPECTED_TYPES = {3: 58, 4: 43, 6: 26, 7: 26, 14: 26, 15: 17, 18: 26}
OPTION_CHANGES = (
    ("monitor_setting.auto_test_channel_enabled", "false"),
    ("AutomaticDisableStatusCodes", "401"),
    ("AutomaticDisableKeywords", ""),
    ("SameChannelRetryTimes", "1"),
    ("CrossChannelRetryTimes", "6"),
    ("RetryTimeBudgetSeconds", "90"),
    ("AutomaticEnableChannelEnabled", "true"),
    ("AutomaticDisableChannelEnabled", "true"),
)
# Explicit configuration fields only: no key, quota, balance, accounting,
# status, status_version, other_info, channel_info, tag, or timestamps copied.
CLONE_FIELDS = (
    "type", "openai_organization", "test_model", "base_url", "other",
    "models", "group", "model_mapping", "status_code_mapping", "setting",
    "param_override", "header_override", "settings",
)
SAFE_CHANNEL_FIELDS = (
    "id", "type", "name", "status", "status_version", "priority", "weight",
    "auto_ban", "models", "group", "test_model",
)
PRESERVED_CONFIG_FIELDS = (
    "type", "name", "models", "group", "priority", "weight", "auto_ban",
    "test_model", "base_url", "model_mapping", "status_code_mapping",
    "setting", "settings", "param_override", "header_override", "remark",
)


class ConfigError(Exception):
    """Messages deliberately contain no HTTP response body or credentials."""


def require(condition, message):
    if not condition:
        raise ConfigError(message)


def json_object(value, label):
    if isinstance(value, dict):
        return copy.deepcopy(value)
    try:
        parsed = json.loads(value)
    except (TypeError, ValueError):
        raise ConfigError(label + " is not valid JSON")
    require(isinstance(parsed, dict), label + " must be an object")
    return parsed


def encode(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def load_env(path):
    values = {}
    with open(str(path), "r", encoding="utf-8") as stream:
        for line in stream:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("export "):
                line = line[7:].lstrip()
            if "=" in line:
                key, value = line.split("=", 1)
                value = value.strip()
                if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                    value = value[1:-1]
                values[key.strip()] = value
    require(bool(values.get("API_TOKEN")), "scheduler.env has no API_TOKEN")
    return values


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


class API(object):
    def __init__(self, token):
        self.token = token
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method, path, payload=None):
        require(path.startswith("/api/"), "Unexpected API path")
        data = None if payload is None else encode(payload).encode("utf-8")
        request = urllib.request.Request(
            BASE_URL + path, data=data, method=method,
            headers={"Authorization": "Bearer " + self.token,
                     "Content-Type": "application/json"})
        try:
            with self.opener.open(request, timeout=30) as response:
                result = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            # Do not render errors/bodies: an upstream or validation error may
            # echo a credential.  Mutations are never retried automatically.
            raise ConfigError("API {} {} returned HTTP {}; mutation outcome may be unknown".format(
                method, path, error.code))
        except Exception:
            raise ConfigError("API {} {} failed; mutation outcome may be unknown".format(method, path))
        require(isinstance(result, dict) and result.get("success") is True,
                "API {} {} rejected the request (body suppressed)".format(method, path))
        return result.get("data")


def list_channels(api):
    channels = {}
    page = 1
    while True:
        data = api.request("GET", "/api/channel/?p={}&page_size=100&id_sort=true".format(page))
        require(isinstance(data, dict) and isinstance(data.get("items"), list),
                "Unexpected channel-list response")
        items = data["items"]
        for channel in items:
            channel_id = channel.get("id")
            require(isinstance(channel_id, int) and channel_id not in channels,
                    "Duplicate/invalid channel ID in paginated response")
            channels[channel_id] = channel
        require(isinstance(data.get("total"), int), "Channel list has no total")
        if len(channels) >= data["total"]:
            return channels
        require(bool(items), "Incomplete channel-list response")
        page += 1


def get_channel(api, channel_id):
    result = api.request("GET", "/api/channel/{}".format(channel_id))
    require(isinstance(result, dict) and result.get("id") == channel_id,
            "Unexpected channel response for ID {}".format(channel_id))
    require(isinstance(result.get("status_version"), int),
            "Candidate lacks status_version; deploy the reviewed recovery build first")
    return result


def get_options(api):
    data = api.request("GET", "/api/option/")
    require(isinstance(data, list), "Unexpected options response")
    options = {item["key"]: item["value"] for item in data}
    require(all(key in options for key, _ in OPTION_CHANGES),
            "Candidate is missing a required option; refusing partial configuration")
    return options


def response_routes(channel):
    settings = json_object(channel.get("settings"), "channel settings")
    advanced = json_object(settings.get("advanced_custom"), "advanced_custom")
    routes = advanced.get("advanced_routes")
    require(isinstance(routes, list), "advanced_routes must be a list")
    matching = [(index, route) for index, route in enumerate(routes)
                if isinstance(route, dict) and route.get("incoming_path") == "/v1/responses"]
    require(len(matching) == 1, "Expected exactly one existing Baidu responses route")
    return settings, advanced, matching


def baidu_patch(channel):
    require(channel.get("type") == 58, "Baidu channel must remain type 58")
    require(isinstance(channel.get("models"), str), "Baidu models must be CSV")
    models = [model.strip() for model in channel["models"].split(",") if model.strip()]
    # Append without dropping or reordering any existing model identifiers.
    desired_models = channel["models"]
    for model in GLM_MODELS:
        if model not in models:
            desired_models += ("," if desired_models else "") + model
            models.append(model)
    settings, advanced, matching = response_routes(channel)
    _, route = matching[0]
    require(route.get("converter") == "openai_responses_to_openai_chat_completions"
            and route.get("upstream_path") == "/v2/tokenplan/personal/chat/completions",
            "Baidu responses conversion has changed; review before applying")
    route_models = route.get("models", [])
    require(isinstance(route_models, list) and all(isinstance(item, str) for item in route_models),
            "Baidu responses models must be a list")
    new_route_models = list(route_models)
    for model in models:
        if model not in new_route_models:
            new_route_models.append(model)
    patch = {"priority": 100, "weight": 3}
    if desired_models != channel["models"]:
        patch["models"] = desired_models
    if new_route_models != route_models or "models" not in route:
        route["models"] = new_route_models
        if isinstance(settings["advanced_custom"], str):
            settings["advanced_custom"] = encode(advanced)
        else:
            settings["advanced_custom"] = advanced
        patch["settings"] = encode(settings)
    return {key: value for key, value in patch.items() if channel.get(key) != value}


def clone_baidu(source):
    require(source.get("type") == 58, "Template channel #3 is not type 58")
    # The inspected template uses only the route configuration and no static
    # authorization overrides.  Stop if that contract changes rather than copy
    # an old account's header/session into the independent subscription.
    require(not source.get("header_override") and not source.get("param_override")
            and not source.get("setting") and not source.get("other")
            and not source.get("openai_organization"),
            "Baidu template has unexpected account/transport overrides; review required")
    settings, advanced, _ = response_routes(source)
    require(set(settings) == {"advanced_custom"} and set(advanced) == {"advanced_routes"},
            "Baidu template has extra settings; review independent-account isolation")
    for route in advanced["advanced_routes"]:
        require(isinstance(route, dict) and set(route).issubset({
            "incoming_path", "upstream_path", "converter", "models"}),
            "Baidu template has extra route fields; review credential isolation")
    require(source.get("base_url", "").rstrip("/") == "https://qianfan.baidubce.com",
            "Baidu template base URL changed; review required")
    clone = {key: copy.deepcopy(source[key]) for key in CLONE_FIELDS
             if key in source and source[key] is not None}
    clone.update(baidu_patch(source))
    clone.update({"name": BAIDU_NAME, "remark": BAIDU_MARKER,
                  "priority": 100, "weight": 3, "auto_ban": 1,
                  # Deliberate staging status, never copied from the template.
                  "status": 2})
    return clone


def find_baidu2(channels, state=None):
    matches = [c for c in channels.values() if c.get("remark") == BAIDU_MARKER]
    require(len(matches) <= 1, "Multiple Baidu #2 markers found; refusing duplicates")
    if matches:
        result = matches[0]
        require(result.get("type") == 58 and result.get("name") == BAIDU_NAME,
                "Baidu #2 marker belongs to unexpected configuration")
        if state and state.get("baidu2_id") is not None:
            require(result["id"] == state["baidu2_id"], "Baidu #2 ID differs from deployment journal")
        return result
    require(not any(c.get("name") == BAIDU_NAME for c in channels.values()),
            "Baidu #2 name exists without this deployment marker; review instead of duplicating")
    require(not state or (not state.get("create_attempted") and state.get("baidu2_id") is None),
            "Prior create outcome is unresolved or channel was removed; inspect before any retry")
    return None


def safe_snapshot(channel):
    result = {key: channel.get(key) for key in SAFE_CHANNEL_FIELDS}
    if channel.get("type") == 58:
        _, _, matching = response_routes(channel)
        result["responses_routes"] = [{
            "index": index, "incoming_path": route["incoming_path"],
            "models_present": "models" in route,
            "models": copy.deepcopy(route.get("models")),
        } for index, route in matching]
    return result


class Journal(object):
    def __init__(self, directory):
        self.directory = pathlib.Path(directory)
        self.path = self.directory / "apply-config-state.json"
        self.state = None
        self.lock = None

    def open(self):
        # Only --apply reaches this code; dry-run does not create files/locks.
        import fcntl
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(str(self.directory), 0o700)
        fd = os.open(str(self.directory / "apply-config.lock"), os.O_CREAT | os.O_RDWR, 0o600)
        self.lock = os.fdopen(fd, "w")
        try:
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            raise ConfigError("Another configuration apply holds the deployment lock")
        if self.path.exists():
            with self.path.open("r", encoding="utf-8") as stream:
                self.state = json.load(stream)
            require(self.state.get("plan_id") == PLAN_ID and self.state.get("api_base") == BASE_URL,
                    "Journal belongs to another plan or API target")
        return self.state

    def save(self):
        fd, name = tempfile.mkstemp(prefix=".apply-config-", dir=str(self.directory))
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as stream:
                json.dump(self.state, stream, ensure_ascii=False, indent=2, sort_keys=True)
                stream.write("\n")
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(name, str(self.path))
        finally:
            if os.path.exists(name):
                os.unlink(name)

    def record_channel(self, channel):
        self.state.setdefault("after_channels", {})[str(channel["id"])] = safe_snapshot(channel)
        self.save()


def apply_channel_patch(api, channel_id, patch_factory, journal):
    before = get_channel(api, channel_id)
    patch = patch_factory(before)
    if not patch:
        journal.record_channel(before)
        print("Channel {} already matches".format(channel_id))
        return before
    require("status" not in patch and "key" not in patch, "Unsafe field in configuration patch")
    payload = {"id": channel_id}
    payload.update(patch)
    api.request("PUT", "/api/channel/", payload)
    after = get_channel(api, channel_id)
    require(all(after.get(key) == value for key, value in patch.items()),
            "Channel {} read-back did not match requested fields".format(channel_id))
    require(all(before.get(key) == after.get(key) for key in PRESERVED_CONFIG_FIELDS if key not in patch),
            "Channel {} unrelated configuration changed; stop for review".format(channel_id))
    journal.record_channel(after)
    print("Channel {} verified: {}".format(channel_id, ",".join(sorted(patch))))
    return after


def enable_channel(api, channel_id, journal, only_if_staged=False):
    before = get_channel(api, channel_id)
    enabled = journal.state.setdefault("enabled_channels", {})
    intents = journal.state.setdefault("enable_intents", {})
    key = str(channel_id)
    if before["status"] == 1:
        enabled[key] = True
        journal.record_channel(before)
        return
    require(not enabled.get(key),
            "Channel {} was disabled after activation; status preserved".format(channel_id))
    require(before["status"] == 2,
            "Channel {} was quarantined since review; status preserved".format(channel_id))
    if key in intents:
        require(before["status_version"] == intents[key],
                "Channel {} changed after enable intent; status preserved".format(channel_id))
    if only_if_staged:
        # Completed deployments must not silently undo a later human disable
        # or automatic quarantine when the script is run again.
        expected = journal.state.get("baidu2_staged_version")
        require(before["status"] == 2 and before["status_version"] == expected,
                "Baidu #2 is disabled after staging changed; refusing to override it")
    intents[key] = before["status_version"]
    journal.save()
    api.request("POST", "/api/channel/{}/status".format(channel_id), {"status": 1})
    after = get_channel(api, channel_id)
    require(after["status"] == 1, "Channel {} enable read-back failed".format(channel_id))
    enabled[key] = True
    journal.record_channel(after)
    print("Channel {} enabled and verified".format(channel_id))


def fixed_patch(priority, weight):
    desired = {"priority": priority, "weight": weight}
    return lambda channel: {key: value for key, value in desired.items() if channel.get(key) != value}


def run(api, args, journal=None):
    state = journal.state if journal else None
    channels = list_channels(api)
    options = get_options(api)
    require(all(channel_id in channels for channel_id in EXISTING_IDS), "Required channel missing")
    for channel_id, channel_type in EXPECTED_TYPES.items():
        require(channels[channel_id].get("type") == channel_type,
                "Unexpected type for channel {}".format(channel_id))
        require(isinstance(channels[channel_id].get("status_version"), int),
                "Candidate lacks status_version; deploy the reviewed recovery build first")
        require(channels[channel_id].get("auto_ban") == 1,
                "Channel {} auto_ban differs from reviewed setup".format(channel_id))
    existing_baidu2 = find_baidu2(channels, state)
    if state:
        for channel_id in state.get("enabled_channels", {}):
            require(int(channel_id) in channels and channels[int(channel_id)]["status"] == 1,
                    "Previously activated channel is now disabled/missing; review before rerun")
    clone = clone_baidu(channels[3])
    targets = [(3, baidu_patch), (4, fixed_patch(-2, 1)), (15, fixed_patch(100, 4))]
    targets.extend((channel_id, fixed_patch(200, 1)) for channel_id in GLM_IDS)
    if existing_baidu2:
        targets.append((existing_baidu2["id"], baidu_patch))
    print("Target: {}; mode: {}".format(BASE_URL, "APPLY" if args.apply else "DRY-RUN (GET only)"))
    print("Baidu #2: {}".format("reuse ID " + str(existing_baidu2["id"]) if existing_baidu2 else "create independent type 58 channel, then enable"))
    for channel_id, factory in targets:
        patch = factory(channels[channel_id])
        print("Channel {} planned fields: {}".format(channel_id, ",".join(sorted(patch)) or "none"))
    print("Ali #15: enable; existing GLM statuses and other channels: preserve")
    for key, value in OPTION_CHANGES:
        print("Option {} -> {}{}".format(key, repr(value), " (already matches)" if options[key] == value else ""))
    if not args.apply:
        print("Dry-run complete; no API mutations, key-file reads, or backup writes")
        return
    require(journal is not None, "Apply requires a locked journal")
    new_key = None
    if not existing_baidu2:
        # Fail before changing options when the credential has not been staged.
        with open(args.baidu_key_file, "r", encoding="utf-8") as stream:
            new_key = stream.read().strip()
        require(bool(new_key) and "\n" not in new_key and "\r" not in new_key,
                "New Baidu credential file must contain one nonempty key")
    if state is None:
        state = {
            "plan_id": PLAN_ID, "api_base": BASE_URL,
            "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "before_channels": {str(i): safe_snapshot(channels[i]) for i in EXISTING_IDS},
            "before_options": {key: options[key] for key, _ in OPTION_CHANGES},
            "baidu2_id": existing_baidu2["id"] if existing_baidu2 else None,
            "baidu2_created_by_plan": False, "create_attempted": False,
            "after_channels": {}, "after_options": {},
        }
        if existing_baidu2:
            state["before_channels"][str(existing_baidu2["id"])] = safe_snapshot(existing_baidu2)
        journal.state = state
        journal.save()

    # Stop native scheduled tests before enabling any new route, then narrow
    # disable conditions before turning on the global disable switch.
    for key, value in OPTION_CHANGES:
        current_options = get_options(api)
        if current_options[key] != value:
            api.request("PUT", "/api/option/", {"key": key, "value": value})
            require(get_options(api)[key] == value, "Option read-back failed for " + key)
        state["after_options"][key] = value
        journal.save()
        print("Option {} verified".format(key))

    if not existing_baidu2:
        # Re-read immediately before create to catch a concurrent administrator
        # create.  The local file lock also prevents concurrent script runs.
        existing_baidu2 = find_baidu2(list_channels(api), state)
        if not existing_baidu2:
            state["create_attempted"] = True
            state["baidu2_created_by_plan"] = True
            journal.save()  # Save before POST; unknown results never trigger a blind repeat.
            create_payload = copy.deepcopy(clone)
            create_payload["key"] = new_key
            api.request("POST", "/api/channel/", {"mode": "single", "channel": create_payload})
            del create_payload["key"]
            del new_key
            existing_baidu2 = find_baidu2(list_channels(api), state)
            require(existing_baidu2 is not None, "Create returned success but marked channel was not found")
        state["baidu2_id"] = existing_baidu2["id"]
        journal.save()
    baidu2_id = existing_baidu2["id"]
    require(baidu2_id not in EXISTING_IDS, "New subscription ID collides with an existing channel")
    if state.get("baidu2_id") is None:
        state["baidu2_id"] = baidu2_id
        journal.save()

    # Creation copies no status/counters. Check its complete non-secret config
    # before enabling; subsequent reruns preserve manual changes outside scope.
    if state.get("baidu2_created_by_plan") and not state.get("baidu2_activated"):
        created = get_channel(api, baidu2_id)
        require(all(created.get(key) == value for key, value in clone.items() if key != "status"),
                "Staged Baidu #2 does not match the reviewed create payload")
        if created["status"] == 1:
            intent = state.get("enable_intents", {}).get(str(baidu2_id))
            require(intent is not None and created["status_version"] == intent + 1,
                    "Staged Baidu #2 was enabled outside the recorded operation")
            state["baidu2_activated"] = True
            state.setdefault("enabled_channels", {})[str(baidu2_id)] = True
        else:
            require(created["status"] == 2 and created["status_version"] ==
                    state.get("baidu2_staged_version", created["status_version"]),
                    "Staged Baidu #2 changed since the saved version; status preserved")
            state["baidu2_staged_version"] = created["status_version"]
        journal.record_channel(created)

    targets = [(3, baidu_patch), (baidu2_id, baidu_patch),
               (4, fixed_patch(-2, 1)), (15, fixed_patch(100, 4))]
    targets.extend((channel_id, fixed_patch(200, 1)) for channel_id in GLM_IDS)
    for channel_id, factory in targets:
        updated = apply_channel_patch(api, channel_id, factory, journal)
        if channel_id == baidu2_id and state.get("baidu2_created_by_plan") and not state.get("baidu2_activated"):
            state["baidu2_staged_version"] = updated["status_version"]
            journal.save()
    enable_channel(api, 15, journal)
    if state.get("baidu2_created_by_plan") and not state.get("baidu2_activated"):
        enable_channel(api, baidu2_id, journal, only_if_staged=True)
        state["baidu2_activated"] = True
        journal.save()
    else:
        existing = get_channel(api, baidu2_id)
        require(existing["status"] == 1,
                "Existing Baidu #2 is disabled; status preserved, review required")
        state.setdefault("enabled_channels", {})[str(baidu2_id)] = True
        journal.record_channel(existing)

    final_channels = list_channels(api)
    for channel_id, factory in targets:
        require(not factory(final_channels[channel_id]),
                "Final configuration drift for channel {}".format(channel_id))
        journal.record_channel(final_channels[channel_id])
    changed_ids = {channel_id for channel_id, _ in targets}
    for channel_id, before in channels.items():
        if channel_id not in changed_ids:
            require(channel_id in final_channels and all(
                before.get(key) == final_channels[channel_id].get(key) for key in PRESERVED_CONFIG_FIELDS),
                "An untouched channel changed during apply; review required")
    final_options = get_options(api)
    require(all(final_options[key] == value for key, value in OPTION_CHANGES),
            "Final option read-back failed")
    state["completed_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    journal.save()
    print("Apply verified; Baidu #2 ID {}. Key-free journal saved.".format(baidu2_id))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true", help="Issue the reviewed API mutations")
    parser.add_argument("--env-file", default=ENV_FILE)
    parser.add_argument("--baidu-key-file", default=KEY_FILE)
    parser.add_argument("--backup-dir", default=BACKUP_DIR)
    args = parser.parse_args()
    journal = Journal(args.backup_dir) if args.apply else None
    try:
        if journal:
            journal.open()
        env = load_env(args.env_file)
        run(API(env["API_TOKEN"]), args, journal)
    except ConfigError as error:
        print("STOP: " + str(error), file=sys.stderr)
        return 1
    except Exception as error:
        # Avoid traceback/OS error strings that may contain credential values.
        print("STOP: {} (details suppressed; no automatic retry)".format(type(error).__name__), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
