# livekit-sip-operator

A Kubernetes operator that manages [LiveKit](https://livekit.io/) SIP resources declaratively. Define SIP inbound trunks, outbound trunks, dispatch rules, and egress configurations as Kubernetes custom resources, and the operator keeps them in sync with the LiveKit server.

## Overview

Instead of managing SIP resources through the LiveKit API or CLI, this operator lets you declare them as standard Kubernetes manifests. The operator watches for changes and reconciles them against your LiveKit server automatically.

**Key design:** The operator itself requires no configuration. Each custom resource points to a LiveKit server via `spec.livekitRef`, meaning:

- Different resources can target different LiveKit servers
- Credentials are managed as standard Kubernetes Secrets
- No cluster-wide LiveKit configuration is needed

## Custom Resources

| Kind | Description |
|------|-------------|
| `SIPInboundTrunk` | Phone numbers and routing for incoming SIP calls |
| `SIPOutboundTrunk` | SIP provider configuration for outgoing calls |
| `SIPDispatchRule` | Rules that route incoming calls to LiveKit rooms |
| `EgressConfig` | Storage backend configuration (S3, Azure) referenced by dispatch rules for call recording |

## Installation

### Helm (recommended)

```bash
helm install livekit-sip-operator \
  oci://registry-1.docker.io/adrianjh/livekit-sip-operator \
  --version 0.0.1 \
  --namespace livekit-sip-operator-system --create-namespace
```

#### Helm Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Container image repository | `docker.io/adrianjh/livekit-sip-operator` |
| `image.tag` | Image tag (defaults to chart `appVersion`) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `replicaCount` | Number of controller replicas | `1` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `resources.requests.cpu` | CPU request | `10m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `leaderElection.enabled` | Enable leader election | `true` |
| `serviceAccount.name` | Service account name (auto-generated if empty) | `""` |
| `nodeSelector` | Node selector | `{}` |
| `tolerations` | Tolerations | `[]` |
| `affinity` | Affinity rules | `{}` |

### Raw Manifests

```bash
kubectl apply -f https://raw.githubusercontent.com/adrijh/livekit-sip-operator/main/dist/install.yaml
```

This creates the `livekit-sip-operator-system` namespace with the CRDs, RBAC, and controller deployment.

## Usage

### 1. Create a LiveKit credentials Secret

Create a Secret in the namespace where your SIP resources will live:

```bash
kubectl create secret generic livekit-credentials \
  --from-literal=LIVEKIT_API_KEY="YOUR_API_KEY" \
  --from-literal=LIVEKIT_API_SECRET="YOUR_API_SECRET"
```

### 2. Create SIP resources

Each resource references the LiveKit Service and credentials Secret via `spec.livekitRef`:

```yaml
apiVersion: sip.livekit-sip.io/v1alpha1
kind: SIPInboundTrunk
metadata:
  name: my-trunk
spec:
  livekitRef:
    service: livekit-server       # name of the LiveKit K8s Service
    secretName: livekit-credentials
    # port: 7880                  # optional, defaults to 7880
  name: my-inbound-trunk
  numbers:
    - "+15551234567"
  allowedAddresses:
    - "0.0.0.0/0"
```

```yaml
apiVersion: sip.livekit-sip.io/v1alpha1
kind: SIPOutboundTrunk
metadata:
  name: my-outbound
spec:
  livekitRef:
    service: livekit-server
    secretName: livekit-credentials
  name: my-outbound-trunk
  address: sip.provider.example.com
  transport: udp
  numbers:
    - "+15559876543"
```

```yaml
apiVersion: sip.livekit-sip.io/v1alpha1
kind: SIPDispatchRule
metadata:
  name: my-rule
spec:
  livekitRef:
    service: livekit-server
    secretName: livekit-credentials
  name: my-dispatch-rule
  type: individual
  individual:
    roomPrefix: sip-call-
  hidePhoneNumber: true
```

### 3. Call recording with EgressConfig

Dispatch rules can reference an `EgressConfig` to automatically record calls. The `EgressConfig` defines the storage backend, and credentials are pulled from Kubernetes Secrets:

```yaml
apiVersion: sip.livekit-sip.io/v1alpha1
kind: EgressConfig
metadata:
  name: minio-egress
spec:
  s3:
    region: us-east-1
    endpoint: "http://minio.minio.svc.cluster.local:9000"
    bucket: sessions
    forcePathStyle: true
    accessKeyRef:
      name: minio-credentials
    secretKeyRef:
      name: minio-credentials
```

Then reference it from a dispatch rule:

```yaml
apiVersion: sip.livekit-sip.io/v1alpha1
kind: SIPDispatchRule
metadata:
  name: recorded-calls
spec:
  livekitRef:
    service: livekit-server
    secretName: livekit-credentials
  name: recorded-dispatch
  type: individual
  individual:
    roomPrefix: call-
  roomConfig:
    egress:
      room:
        audioOnly: true
        fileOutputs:
          - filepath: "recordings/{room_id}/recording.ogg"
            egressConfigRef:
              name: minio-egress
```

### 4. Check status

```bash
kubectl get sipinboundtrunks
kubectl get sipoutboundtrunks
kubectl get sipdispatchrules
kubectl get egressconfigs
```

Each resource shows its LiveKit ID and readiness status:

```
NAME       TRUNK ID              READY   AGE
my-trunk   ST_xxxxxxxxxxxx       True    5m
```

## Uninstall

### Helm

```bash
helm uninstall livekit-sip-operator -n livekit-sip-operator-system
kubectl delete namespace livekit-sip-operator-system
```

### Raw Manifests

```bash
kubectl delete -f https://raw.githubusercontent.com/adrijh/livekit-sip-operator/main/dist/install.yaml
```

## Development

### Prerequisites

- Go 1.26+
- Docker
- kubectl
- Access to a Kubernetes cluster

### Run locally

```bash
make install   # install CRDs into your cluster
make run       # run the controller on your machine
```

### Build and deploy to a local cluster

```bash
make docker-build                          # builds controller:latest for local platform
make deploy IMG=controller:latest          # deploys to current kubectl context
```

### Run tests

```bash
make test       # unit tests
make test-e2e   # e2e tests (uses Kind)
```
