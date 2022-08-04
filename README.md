# Noe

Noe is a Kubernetes mutating webhook that assigns node architectures matching the image needs.

When a new Pod is created in the Cluster, the webhook will be called to review and adjust the Pod spec as needed. It then takes all the images for the Pod and checks with their registry for the metadata on those images for the supported CPU architectures. It will then add node affinities to the Pod according to the supported CPU architectures, ensuring the Pod will only be scheduled in nodes capable of executing all the images.


# Running tests

All tests can be run using the plain `go test ./...`

# Installing noe

Currently, noe provides a helm chart, available from the code repository exclusively.
The simplest way to install it is to use ArgoCD and define an application such as:

```
apiVersion: argoproj.io/v1alpha1
kind: Application
spec:
  source:
    repoURL: https://github.com/adevinta/noe.git
    path: charts/noe
    targetRevision: HEAD
```

