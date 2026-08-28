# Static binary in an empty image: the init container needs nothing
# else (the kubelet mounts the inputs and the state volume).
# Pinned by digest (golang:1.26, resolved 2026-08-27): the builder
# image controls the output binary, so it is pinned like a dependency.
FROM golang:1.26@sha256:dc2521c2a906db43073b8b4d99f491b6341cf15610b6ebbab187c45153f9959e AS build
ENV GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /frigatecfg .

FROM scratch
COPY --from=build /frigatecfg /frigatecfg
ENTRYPOINT ["/frigatecfg"]
