# Noe: Kubernetes Mutating Webhook for Node Architecture Selection

Noe is a [Kubernetes mutating webhook](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/) that dynamically assigns node architectures to match the requirements of container images within a Pod. It simplifies mixed-architecture deployments (e.g. ARM and x86) by ensuring that Pods are scheduled on nodes capable of executing all their images.

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