# Publish through the local Aster agent

GoFi's `aster` sink calls the Aster application agent's unary Connect `PublishEvent` API. Aster owns the cryptography, mesh custody, provisioning, and durable local Event authority. GoFi supplies the selected application payload.

The implementation follows the upstream [application schema](https://github.com/defenseunicorns/aster/blob/main/proto/aster/application/v1alpha1/aster.proto), [agent quickstart](https://github.com/defenseunicorns/aster/blob/main/docs/quickstart/connect-agent.md), and [Connect example](https://github.com/defenseunicorns/aster/blob/main/examples/connect_agent.sh).

## Sink configuration

```yaml
type: aster
url: http://127.0.0.1:8181
bodyMode: raw
aster:
  topic: application.telemetry
  scope: mission/apps
  priority: routine
  logicalKeyField: app
  tokenFile: /run/secrets/aster/client.token
  timeoutSeconds: 10
```

`topic` and `scope` are required. Priority is `routine` (default), `priority`, `immediate`, or `flash`. Use a fixed UTF-8 `logicalKey` or `logicalKeyField` selecting a parsed string field; omit both for an empty key. Missing or non-string configured key fields fail publication.

The default agent origin is `http://127.0.0.1:8181`. GoFi requires a literal loopback IP, explicit port, HTTP, and no URL path/query/credentials. It rejects remote hosts, redirects, and environment proxy routing. The RPC path is `/aster.application.v1alpha1.AsterApplicationService/PublishEvent`.

Provide one inline `token` or a mounted absolute `tokenFile`. If neither is configured, GoFi uses `GOFI_ASTER_TOKEN_FILE`. Token files are reread on each send to allow atomic credential replacement. The file must be readable inside the actual listener process/container. `timeoutSeconds` defaults to 10 and accepts 1–30.

`bodyMode: raw` sends original wire bytes and is the default. `wrapped` sends a JSON envelope containing decoded `fields` and `meta`. Protobuf JSON encodes operation keys, logical keys, and payload bytes as base64; users provide the logical key as ordinary text.

## Acceptance and recovery boundary

GoFi generates an in-memory identity at ingest and derives a stable operation key for that message and destination selector. Sending the same Message again uses the same operation key. Different ingest events receive different keys. GoFi does not automatically retry failed sink sends, journal pending requests, retain receipts across restart, or resume uncertain effects after a crash.

Success requires a complete Aster `PublishEvent` receipt, including the Event identity and local acceptance marker. This represents the upstream agent's local durable acceptance contract; it does not prove remote delivery or application consumption. Network errors can leave publication outcome unknown. Do not resubmit uncertain application effects as new traffic and assume they will deduplicate: new ingestion has new identity. A crash-safe application must persist its effect identity and receipt using an appropriate outbox/journal outside this adapter.

Protocol tests cover exact headers and protobuf JSON, stable retry keys, token rotation, origin restrictions, redirects, failures, and incomplete receipts. They do not establish a live protected-provider deployment or physical mesh acceptance.

## Runtime placement

The upstream agent exposes plaintext loopback for its application and health listeners. Supported customer placement requires a dedicated network namespace containing the agent and its trusted GoFi application. Do not create an agent Service, ingress, host-port mapping, or remote tunnel. Bearer authentication remains required inside the namespace. See the upstream [deployment boundary](https://github.com/defenseunicorns/aster/blob/main/docs/quickstart/connect-agent.md#customer-support-boundary).

The stock upstream development adapter and unprotected reference fixture are development-only. Supply the deployment owner's protected-provider-composed agent binary/image and its provisioning configuration; this repository does not invent or ship a production provider image.

### Binary

Run the GoFi binary and the provider-composed agent under a supervisor in the same dedicated Linux network namespace. The supervisor owns namespace creation, permitted mesh connectivity, credential mounts, agent configuration, persistent state, startup readiness, and shutdown ordering. Bind the agent application API to `127.0.0.1:8181`; point the GoFi sink there. Running both on an ordinary host's shared loopback is only a development arrangement.

### Docker Compose

For an explicit shared namespace, run listeners in GoFi's process and attach the agent to that service's network namespace. This is an illustrative Compose fragment; substitute an operator-supplied provider image and real mounted configuration:

```yaml
services:
  gofi:
    image: gofi:local
    environment:
      GOFI_DRIVER: inprocess
      GOFI_CONFIG_FILE: /var/lib/gofi/listeners.yaml
      GOFI_ASTER_TOKEN_FILE: /run/secrets/aster/client.token
    ports:
      - "9090:9090"
      - "7000:7000"
    volumes:
      - ./state:/var/lib/gofi
      - ./aster-secrets:/run/secrets/aster:ro
  aster:
    image: ${ASTER_PROVIDER_IMAGE:?set an approved provider-composed image}
    network_mode: service:gofi
    command: ["--config", "/etc/aster/agent.json"]
    volumes:
      - ./aster-config:/etc/aster:ro
      - ./aster-secrets:/run/secrets/aster:ro
      - ./aster-state:/var/lib/aster
```

The GoFi service publishes its own portal and application data ports. The agent has no published ports. Do not use the default Docker driver for this single-companion arrangement: it creates separate listener containers whose loopback interfaces are separate from the controller. Coordinate readiness before sending traffic; GoFi's sink is not an agent supervisor.

### Kubernetes

Use an Aster sidecar in each listener Pod. Pod containers share a network namespace, so that listener's agent is reachable on loopback. Configure the chart's `listenerPodTemplate` to add the agent container and its volumes, and mount the bearer credential into the main GoFi listener as well:

```yaml
listenerPodTemplate:
  env:
    - name: GOFI_ASTER_TOKEN_FILE
      value: /run/secrets/aster/client.token
  volumeMounts:
    - name: aster-credentials
      mountPath: /run/secrets/aster
      readOnly: true
  containers:
    - name: aster
      image: YOUR_REGISTRY/provider-composed-aster:FIXED_VERSION
      args: ["--config", "/etc/aster/agent.json"]
      volumeMounts:
        - name: aster-credentials
          mountPath: /run/secrets/aster
          readOnly: true
        - name: aster-config
          mountPath: /etc/aster
          readOnly: true
  volumes:
    - name: aster-credentials
      secret:
        secretName: aster-credentials
    - name: aster-config
      configMap:
        name: aster-config
```

This fragment shows wiring only. The operator must also supply the agent's protected provisioning and exclusive persistent state volume, ownership/security settings, readiness probes, and any approved mesh connectivity. Every managed listener receives the configured template. Give each agent exclusively owned durable state; multiple agents must not open the same store. Ephemeral storage loses that agent's durable state when its Pod is replaced. The generic template does not allocate a per-listener PVC or qualify an agent deployment automatically.


## Configuration validation and package status

The controller validates the Aster sink's origin, selector, timeout, and token
configuration without opening a connection or reading a listener-only credential
mount. The actual listener reads and validates that mount when its sink starts.
This permits a Kubernetes controller to accept a valid declarative resource
without requiring the agent token on the controller filesystem.

The generic GoFi package wires the portal and application runtime. It does not
supply or qualify an Aster protected-provider deployment. UDS runtime integration
and package validation remain subject to the outstanding gates in
[verification evidence](verification.md).
