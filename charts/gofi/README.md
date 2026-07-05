# gofi

A Helm chart for the GoFi data-ingress controller.

## Install

```
helm install gofi ./charts/gofi
```

## Values that matter

- `image.tag` - defaults to `latest`; pin this to a specific released tag for
  anything other than local experimentation.
- `replicaCount` - **must stay at `1`**. The controller keeps its
  reconciliation state (`controller.Controller`) in memory only, with no
  leader election or shared state store, so running more than one replica
  will cause multiple controllers to race over the same listener Pods.
- `rbac.create` - when `true` (default), installs a namespaced `Role` and
  `RoleBinding` granting the controller's ServiceAccount permission to
  `get`/`list`/`watch`/`create`/`delete` `pods` and `get` `pods/log` in its own
  namespace. This anticipates the Kubernetes operator driver
  (`driver/kube.go`), which will spawn one Pod per listener definition. If you
  disable this, the operator driver will not be able to manage listener Pods.
- `serviceAccount.create` / `serviceAccount.name` - controls whether a
  ServiceAccount is created for the controller, or an existing one is used.
- `service.port` - port for both the Service and the container's REST
  API/embedded UI (default `9090`).

## Deployment mode

This chart always sets `GOFI_DEPLOY_MODE=kubernetes` on the controller
container, in addition to the standard in-cluster `KUBERNETES_SERVICE_HOST`
env var Kubernetes sets automatically.

## Uninstall

```
helm uninstall gofi
```
