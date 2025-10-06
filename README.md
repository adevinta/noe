# Noe: Kubernetes Mutating Webhook for Node Architecture Selection

**[Blog post announcing and explaining the effort behind Noe](https://medium.com/adevinta-tech-blog/transparently-providing-arm-nodes-to-4-000-engineers-c09c92314f2f)**

Noe is a [Kubernetes mutating webhook](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/) that dynamically assigns node architectures to match the requirements of container images within a Pod. It simplifies mixed-architecture deployments (e.g. ARM and x86) by ensuring that Pods are scheduled on nodes capable of executing all their images.

![Overview diagram](/noe_global_diagram.svg)

## Features

- Automatically adjusts node affinities based on container images' supported architectures
- Improves deployment efficiency by removing the need for manual node selector configuration
- Facilitates seamless mixed-architecture deployments by ensuring compatibility between ARM and x86 nodes

## Running Tests

Run all tests using the following command:

```bash
go test ./...
```

## Installing Noe

Noe provides a [Helm](https://helm.sh/) chart, available exclusively from the code repository. The simplest way to install it is to use [ArgoCD](https://argo-cd.readthedocs.io/en/stable/) and define an application such as:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
spec:
  source:
    repoURL: https://github.com/adevinta/noe.git
    path: charts/noe
    targetRevision: HEAD
```

### Helm chart values

Noe's Helm chart is designed to work in standard configurations.
Below is a comprehensive guide on how to customize the Helm chart values to
match your Kubernetes configuration.

### Customise Noe deployed image

This section defines the Docker image details used by the deployment.

```yaml
image:
  registry: ghcr.io
  repository: adevinta/noe
  tag: latest
```

### Manage docker registries rate limits

Forces the use of registry proxies for specific images.
This helps better manage the requests to public docker registries and prevent
requests to be rate limited, or suffer from registries downtime.

Default:

```yaml
proxies: []
```

Example:
```yaml
proxies:
  - docker.io=docker-proxy.company.corp
  - quay.io=quay-proxy.company.corp
```

### Further pod scheduling constraints

#### Ensure pod and nodes have similar labels

Specify a list of label names that pods must have in common with the node they run on.
Those labels constraints are added to the node selectors computed by the architectures
images supports.

Default:

```yaml
matchNodeLabels: []
```

Example:
```yaml
matchNodeLabels:
  - kubernetes.io/arch
  - failure-domain.beta.kubernetes.io/region
```

With this configuration, a pod with label `failure-domain.beta.kubernetes.io/region=eu-west-3`
would only be scheduled on nodes with label `failure-domain.beta.kubernetes.io/region=eu-west-3`.
Pods without any `failure-domain.beta.kubernetes.io/region` label will be scheduled on any node.

#### Restrict image architectures

List of architectures that can be scheduled. Any other architecture supported by images will be ignored.

Default:

```yaml
schedulableArchitectures: []
```

Example:

```yaml
schedulableArchitectures:
  - amd64
  - arm64
```

### Configuring accesses to private images

While Noe handles the `imagePullSecret` fields, it can also be configured to transparently authenticate
requests to private registries.
Because of its design, it considers that node-level private registry authentication is consistent across the whole cluster.

#### kubeletConfig

Configuration for the kubelet credentials configuration.
All those paths will automatically be mounted from the host to noe's container
so Noe can retrieve image configurations.

Default:

```yaml
kubeletConfig:
```

Example:

```yaml
kubeletConfig:
  binDir: /etc/eks/image-credential-provider
  configDir: /etc/eks/image-credential-provider
  config: config.json
```

#### containerdConfigPathCandidates

Paths to the containerd configuration files.
All those paths will automatically be mounted from the host to noe's container
so Noe can retrieve image configurations.

Default:

```yaml
containerdConfigPathCandidates:
  - /etc/containerd
```

#### dockerConfigPathCandidates

This setting specifies the possible paths where the configuration files
using the Docker format can be found on the host.
Specifying those values will automatically mount the host paths inside
Noe's containers.

Default:

```yaml
dockerConfigPathCandidates:
  - /var/lib/kubelet/config.json
```

### Additional metadata

You can customize the labels and annotations of Kubernetes objects as followed.
Customizable objects are: `pod`, `issuer`, `certificate`, `mutatingwebhookconfiguration`, `rolebinding`, `clusterrole`, `clusterrolebinding`, `serviceaccount`, `deployment`

Default:

```yaml
pod:
  # labels:
  #   some: label
  # annotations:
  #   some: annotations
```

Example:
```yaml
pod:
  labels:
    app: my-application
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
```

## Hinting preferred and supported target architectures

By default, Noe will automatically select the appropriate architecture when only one is supported by all the containers in the Pod. 
If more than one is available, Noe will select the system-defined preferred one if available. This preference can be chosen in the command line for Noe (defaults to `amd64` if unspecified): 
```
./noe -preferred-arch amd64
```

This preference can also be overridden at the Pod level by adding the label:
```
labels:
  arch.noe.adevinta.com/preferred: amd64
```

Noe will always prioritize a running Pod, so if the preference is not supported by all the containers in the Pod, the common architecture will be selected.

You can restrict the acceptable common architectures in the command line for Noe:
```
./noe -cluster-schedulable-archs amd64,arm64
```

If you specify both a preferred architecture and a list of supported architectures in the command line, the default architecture must be part of the list. Otherwise Noe will fail to start.

If a preferred architecture is specified at the Pod level and is not compatible with the supported architectures listed in the command line, it will be ignored.

## Troubleshooting guide


### Image inspection

This guide explain how to inspect container images to verify the supported architectures in case Noe's selection is not as expected.

1. Authenticate with the registry (if required)

```bash
# in case of using docker
docker login <registry-url>
```

2. Inspect the manifest

```bash
docker manifest inspect <regitry>/<repository>/<image>:<tag>
```

for example:

```bash
docker manifest inspect docker.io/fluent/fluent-bit:2.1.10
```

You should find the detail such as

```json
{
"manifests": [
  {
    "platform": {
      "architecture": "amd64",
      "os": "linux"
    }
  },
  {
    "platform": {
      "architecture": "arm64",
      "os": "linux"
    }
  }
]
}
```

## High Availability (HA) Deployment

Noe supports High Availability deployment with leader election to ensure zero-downtime webhook processing.

### HA Architecture

- **Webhook Component**: All replicas actively process admission requests (load balanced by Kubernetes service)  
- **Controller Component**: Only the leader processes pod reconciliation/eviction (followers standby)

### Configuration

```yaml
ha:
  enabled: true  # Automatically: 2 replicas + leader election + PDB
```

**When `ha.enabled=true`:**
- Automatically sets `replicas: 2`
- Enables leader election with sensible defaults
- Creates PodDisruptionBudget with `minAvailable: 1`
- Adds pod anti-affinity rules to spread replicas across nodes

**When `ha.enabled=false` (default):**  
- Single replica deployment
- No leader election overhead
- Suitable for development/testing