"""Send one deliberate event and verify its actual persisted pipeline output."""
import json
import time
import urllib.request
import urllib.parse
import uuid

source = "http://127.0.0.1:19080/events"
receiver = "http://127.0.0.1:19081/events"
event = {"eventId": str(uuid.uuid4()), "app": "test-request", "message": "explicit end-to-end verification", "status": 201}
request = urllib.request.Request(source, json.dumps(event).encode(), {"Content-Type": "application/json"})
with urllib.request.urlopen(request, timeout=10) as response:
    if response.status != 202:
        raise SystemExit("sender did not accept verification event")
for attempt in range(50):
    with urllib.request.urlopen(receiver, timeout=5) as response:
        result = json.load(response)
    matching = [record for record in result["events"] if record.get("fields", {}).get("eventId") == event["eventId"]]
    if matching:
        fields = matching[-1]["fields"]
        expected = {**event, "observedBy": "gofi"}
        if fields != expected:
            raise SystemExit(f"received fields differ: {fields!r}")
        received = matching[-1]
        record_count = result["count"]
        break
    time.sleep(0.1)
else:
    raise SystemExit("verification event was not delivered to the receiver")

query = urllib.parse.urlencode({"query": 'gofi_frames_received_total{listener="real-events"}'})
for attempt in range(60):
    with urllib.request.urlopen("http://127.0.0.1:19094/api/v1/query?" + query, timeout=5) as response:
        metrics = json.load(response)["data"]["result"]
    with urllib.request.urlopen("http://127.0.0.1:19094/api/v1/targets", timeout=5) as response:
        targets = json.load(response)["data"]["activeTargets"]
    target_health = {target["labels"]["job"]: target["health"] for target in targets}
    if metrics and float(metrics[0]["value"][1]) > 0 and all(target_health.get(job) == "up" for job in ("gofi-controller", "gofi-listener")):
        print(json.dumps({"verified": True, "sent": event, "received": received, "persistedRecordCount": record_count, "prometheusFramesReceived": float(metrics[0]["value"][1]), "prometheusTargets": target_health}, indent=2))
        break
    time.sleep(0.2)
else:
    raise SystemExit("event was delivered but Prometheus did not scrape both GoFi targets and the actual frame counter")
