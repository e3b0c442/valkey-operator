# valkey-operator

A Kubernetes operator for deploying and managing Valkey instances using Custom Resources.

## Description

The valkey-operator provides a Kubernetes Custom Resource Definition (CRD) for managing Valkey instances. The operator follows a state machine approach to reconcile Valkey resources, deploying them as StatefulSets with headless Services.

### Features

- **Single-replica Valkey deployment**: Deploys Valkey as a StatefulSet with one replica
- **State machine reconciliation**: Uses a Progressing condition to track reconciliation state
- **Status conditions**: Provides Available, Progressing, and Degraded conditions for observability
- **Headless Service**: Automatically creates a headless Service for StatefulSet pod discovery
- **Default configuration**: Uses official Valkey image (`valkey/valkey:latest`) with default settings

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/valkey-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/valkey-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

This will create a Valkey instance named `valkey-sample`. The operator will:
1. Create a headless Service for the StatefulSet
2. Create a StatefulSet with a single Valkey replica
3. Update status conditions as the deployment progresses

**Check the status of your Valkey instance:**

```sh
kubectl get valkey valkey-sample -o yaml
```

The status will show conditions:
- **Available**: True when the StatefulSet is ready
- **Progressing**: Tracks the current reconciliation state (CreatingService, CreatingStatefulSet, WaitingForReady)
- **Degraded**: True if there are errors preventing normal operation

**Connect to Valkey:**

The headless Service is accessible at `<valkey-name>.<namespace>.svc.cluster.local:6379`. For example:

```sh
kubectl run -it --rm redis-client --image=redis:7-alpine -- redis-cli -h valkey-sample.default.svc.cluster.local -p 6379
```

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/valkey-operator:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/valkey-operator/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

