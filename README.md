# livekit-sip-operator

A Kubernetes operator that manages [LiveKit](https://livekit.io/) SIP resources declaratively. Define SIP inbound trunks, outbound trunks, and dispatch rules as Kubernetes custom resources, and the operator keeps them in sync with the LiveKit server.

## Custom Resources

| Kind | Description |
|------|-------------|
| `SIPInboundTrunk` | Phone numbers and routing for incoming SIP calls |
| `SIPOutboundTrunk` | SIP provider configuration for outgoing calls |
| `SIPDispatchRule` | Rules that route incoming calls to LiveKit rooms |

Each CR points to a LiveKit server via `spec.livekitRef`, which includes the in-cluster Service name and a Secret with API credentials. This means:

- The operator deploys with zero configuration
- Different resources can target different LiveKit servers
- Credentials are managed as standard Kubernetes Secrets

## Getting Started

### Prerequisites

- go version v1.24.0+
- docker version 17.03+
- kubectl version v1.11.3+
- Access to a Kubernetes v1.11.3+ cluster
- A running LiveKit server with SIP enabled

### 1. Deploy the operator

```sh
make docker-build docker-push IMG=<your-registry>/livekit-sip-operator:tag
make deploy IMG=<your-registry>/livekit-sip-operator:tag
```

### 2. Create a LiveKit credentials secret

Create a Secret in the namespace where your SIP resources will live. Only API credentials are needed — the server URL is derived from the Service name.

```sh
kubectl create secret generic livekit-credentials \
  --from-literal=LIVEKIT_API_KEY="YOUR_API_KEY" \
  --from-literal=LIVEKIT_API_SECRET="YOUR_API_SECRET"
```

### 3. Create SIP resources

Each resource references the LiveKit Service and credentials secret via `spec.livekitRef`:

```yaml
apiVersion: sip.livekit-sip.io/v1alpha1
kind: SIPInboundTrunk
metadata:
  name: my-trunk
spec:
  livekitRef:
    service: livekit-server    # name of the LiveKit K8s Service
    secretName: livekit-credentials
    # port: 7880               # optional, defaults to 7880
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

Or apply the included samples:

```sh
kubectl create secret generic livekit-credentials \
  --from-literal=LIVEKIT_API_KEY="YOUR_API_KEY" \
  --from-literal=LIVEKIT_API_SECRET="YOUR_API_SECRET"

kubectl apply -f config/samples/
```

### 4. Check status

```sh
kubectl get sipinboundtrunks
kubectl get sipoutboundtrunks
kubectl get sipdispatchrules
```

## Local Development

Run the controller locally against your cluster (no image build needed):

```sh
make install   # install CRDs
make run       # run controller locally
```

Then create a credentials secret and apply your CRs as above.

## Uninstall

```sh
kubectl delete -k config/samples/   # delete sample CRs
make undeploy                        # remove controller + RBAC + CRDs
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
