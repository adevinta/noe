# syntax=docker/dockerfile:1
# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.22 as builder
ARG TARGETPLATFORM
WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY vendor/ vendor/

# Copy the go source
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOARCH=$(echo ${TARGETPLATFORM:-linux/amd64} | cut -d/ -f2) go build -mod=vendor -a -o manager ./cmd/noe/main.go
# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
# FROM gcr.io/distroless/static:nonroot
FROM debian
WORKDIR /
RUN apt-get update && apt-get install -y ca-certificates
COPY --from=builder /workspace/manager .
# USER nonroot:nonroot
ENTRYPOINT ["/manager"]
