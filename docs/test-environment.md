# Real application test environment

This stack runs an HTTP application, a GoFi controller with a separate Docker
listener container, a durable HTTP receiver, and Prometheus. The application sends events
only when a user submits a request. Traffic counts and receiver records come
from actual TCP frames and HTTP writes.

```text
User HTTP POST → application:8080 → GoFi TCP listener:17000
                                   → JSON parser
                                   → observedBy="gofi" transform
                                   → receiver:8080/events → persistent JSONL
```

From the repository root, build the current GoFi image and start the stack:

```sh
docker build -t ghcr.io/wcrum/gofi:0.2.0 .
python3 testenv/manage.py up
```

| Endpoint | Purpose |
| --- | --- |
| [localhost:19090](http://localhost:19090) | GoFi portal and ConnectRPC management |
| [localhost:19080](http://localhost:19080) | Application form for sending actual events |
| [localhost:19081](http://localhost:19081) | Receiver view |
| [localhost:19081/events](http://localhost:19081/events) | Persisted event count and latest 1000 records |
| [localhost:19090/metrics](http://localhost:19090/metrics) | Controller Prometheus metrics |
| [localhost:19094](http://localhost:19094) | Prometheus query UI and actual runtime counters |
| `localhost:17000` | Published GoFi TCP source |

The Compose file uses named network `gofi-test` so its application and receiver
can reach the listener container launched by GoFi's Docker driver. The portal
and application HTTP ports bind to loopback. The Docker driver publishes the
listener's configured source port on the host.

Send an event through the application:

```sh
curl -sS http://localhost:19080/events \
  -H 'Content-Type: application/json' \
  -d '{"eventId":"my-first-event","app":"my-app","message":"actual request","status":201}'
curl -sS http://localhost:19081/events
```

Open the `real-events` listener in the GoFi flow to watch that request. The
receiver record contains the original event fields plus `observedBy: gofi`.
The sender's HTTP 202 means it wrote to the TCP source; inspect the receiver
to confirm delivery through the complete pipeline.

For one explicit end-to-end verification event:

```sh
python3 testenv/manage.py verify
```

This sends one unique event, waits for its actual persisted receiver record,
and asserts that every input field and the GoFi transform match. It prints
the sent and received payloads, checks both Prometheus scrape targets are up,
and verifies the listener frame counter is positive. It does not start a traffic generator.

Because listener pipelines run in separate containers, their traffic metrics
are served on their relay port inside the test network. To inspect them with
the application's already available Python runtime:

```sh
docker compose -f compose.test.yaml exec -T application python -c \
  'import urllib.request; print(urllib.request.urlopen("http://gofi-listener-real-events:9091/metrics").read().decode())'
```

Stop the stack while preserving listener configuration and received records:

```sh
python3 testenv/manage.py stop
```

Use the helper because GoFi manages the listener child outside Compose. It
stops the controller before removing the stack's managed listener, then shuts
down Compose. Named volumes preserve configuration, receiver JSONL, and Prometheus history across
starts. Initialization copies `testenv/listeners.yaml` only when the config
volume is empty; later changes made through GoFi remain durable. To begin with
empty configuration and records, deliberately remove the named volumes with
`docker compose -f compose.test.yaml down -v` after stopping the stack.

Prometheus scrapes the controller and separate listener every two seconds.
`gofi_frames_received_total{listener="real-events"}` counts actual frames over
the listener process lifetime. Restarting that process resets its counter;
Prometheus preserves history and `rate(...)` handles counter resets. These
values do not depend on whether a browser has opened the live flow.

To exercise the complete Python library control plane with actual traffic:

```sh
python3 testenv/manage.py verify-python
```

This creates a temporary durable library through `SandboxService`, discovers
its registered parser and transformer, attaches them to a separate Docker
listener on port `17001`, sends one explicit TCP event, and checks the
receiver's persisted `python: verified` output. It then removes only that
temporary flow and library; the main `real-events` flow stays running.
