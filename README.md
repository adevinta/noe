# Noe: Kubernetes Mutating Webhook for Node Architecture Selection

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

## Hinting preferred target architecture

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
